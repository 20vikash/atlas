from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

import atlas.service.core.cargo.bucket as bucket
from atlas.service.core.cargo.bucket import CargoBucket

CREDENTIALS = {"access_key": "GKc6d95fb2", "secret_access_key": "cf076172d1"}


def atlas_settings(**values) -> SimpleNamespace:
	defaults = {
		"region_id": 3,
		"region_name": "blr",
		"wildcard_domain": "example.com",
		"cargo_audience_id": "atlas-cargo:3",
		"is_object_storage_configured": False,
		"object_storage_bucket": None,
		"save": Mock(),
	}
	return SimpleNamespace(**(defaults | values))


def cargo_bucket(**values) -> CargoBucket:
	with patch.object(bucket.frappe, "get_single", return_value=atlas_settings(**values)):
		return CargoBucket()


class TestCargoBucketNaming(UnitTestCase):
	def test_the_bucket_is_named_for_its_region(self) -> None:
		self.assertEqual(cargo_bucket(region_name="BLR").name, "atlas-blr")

	def test_uploads_use_the_public_s3_endpoint(self) -> None:
		self.assertEqual(cargo_bucket().endpoint_url, "https://s3-svc.example.com")


class TestCargoBucketReadiness(UnitTestCase):
	def test_garage_is_checked_on_its_admin_domain(self) -> None:
		instance = cargo_bucket()
		with patch.object(bucket.requests, "get") as request:
			request.return_value.ok = True
			self.assertTrue(instance.is_garage_ready)

		request.assert_called_once_with("https://s3-admin-svc.example.com/health", timeout=5)

	def test_an_unreachable_garage_is_not_ready(self) -> None:
		instance = cargo_bucket()
		with patch.object(bucket.requests, "get", side_effect=bucket.requests.RequestException):
			self.assertFalse(instance.is_garage_ready)

	def test_provisioning_waits_quietly_while_garage_is_down(self) -> None:
		instance = cargo_bucket()
		with (
			patch.object(instance.__class__, "is_garage_ready", new=property(lambda self: False)),
			patch.object(instance, "create") as create,
		):
			instance.provision()

		create.assert_not_called()


class TestCargoBucketCreation(UnitTestCase):
	def test_the_request_carries_a_cargo_token_and_the_region(self) -> None:
		instance = cargo_bucket()
		with (
			patch.object(bucket, "issue_token", return_value="cargo-token"),
			patch.object(bucket.requests, "post") as request,
		):
			request.return_value.ok = True
			request.return_value.json.return_value = {"message": {"credentials": CREDENTIALS}}
			credentials = instance.create()

		request.assert_called_once_with(
			"https://cargo.example.com/api/method/cargo.object_storage.api.bucket.create_bucket",
			headers={"X-Cargo-Access-Token": "cargo-token"},
			json={"name": "atlas-blr", "region": "blr"},
			timeout=30,
		)
		self.assertEqual(credentials, CREDENTIALS)

	def test_the_token_names_the_regional_cargo_audience(self) -> None:
		instance = cargo_bucket()
		with patch.object(bucket, "issue_token", return_value="cargo-token") as issue:
			instance.access_token()

		self.assertEqual(issue.call_args.kwargs["audience"], "atlas-cargo:3")
		self.assertEqual(issue.call_args.kwargs["subject"], "atlas")
		self.assertEqual(issue.call_args.kwargs["tenant"], "0")

	def test_a_refused_request_stops_provisioning(self) -> None:
		instance = cargo_bucket()
		with (
			patch.object(bucket, "issue_token", return_value="cargo-token"),
			patch.object(bucket.requests, "post") as request,
			self.assertRaisesRegex(frappe.ValidationError, "atlas-blr"),
		):
			request.return_value.ok = False
			request.return_value.status_code = 403
			instance.create()

	def test_the_returned_key_is_stored_at_once(self) -> None:
		instance = cargo_bucket()
		with patch.object(bucket.frappe.db, "commit") as commit:
			instance.store(CREDENTIALS)

		settings = instance.settings
		self.assertEqual(settings.object_storage_bucket, "atlas-blr")
		self.assertEqual(settings.object_storage_endpoint_url, "https://s3-svc.example.com")
		self.assertEqual(settings.object_storage_region, "blr")
		self.assertEqual(settings.object_storage_access_key_id, "GKc6d95fb2")
		self.assertEqual(settings.object_storage_secret_access_key, "cf076172d1")
		settings.save.assert_called_once_with(ignore_permissions=True)
		commit.assert_called_once()


class TestCargoBucketOverwrite(UnitTestCase):
	def test_a_configured_bucket_is_never_replaced(self) -> None:
		instance = cargo_bucket(is_object_storage_configured=True, object_storage_bucket="atlas-blr")
		with (
			patch.object(bucket.frappe, "log_error") as log_error,
			patch.object(instance, "create") as create,
		):
			instance.provision()

		create.assert_not_called()
		self.assertIn("atlas-blr", log_error.call_args.kwargs["message"])


class TestCargoBucketScheduling(UnitTestCase):
	def test_a_configured_region_queues_nothing(self) -> None:
		with (
			patch.object(
				bucket.frappe, "get_single", return_value=atlas_settings(is_object_storage_configured=True)
			),
			patch.object(bucket, "enqueue_bucket_provisioning") as enqueue,
		):
			bucket.enqueue_pending_bucket_provisioning()

		enqueue.assert_not_called()

	def test_nothing_is_queued_before_cargo_serves(self) -> None:
		with (
			patch.object(bucket.frappe, "get_single", return_value=atlas_settings()),
			patch.object(bucket.frappe.db, "get_single_value", return_value="Provisioning"),
			patch.object(bucket, "enqueue_bucket_provisioning") as enqueue,
		):
			bucket.enqueue_pending_bucket_provisioning()

		enqueue.assert_not_called()

	def test_an_active_cargo_without_object_storage_is_queued(self) -> None:
		with (
			patch.object(bucket.frappe, "get_single", return_value=atlas_settings()),
			patch.object(bucket.frappe.db, "get_single_value", return_value="Active"),
			patch.object(bucket, "enqueue_bucket_provisioning") as enqueue,
		):
			bucket.enqueue_pending_bucket_provisioning()

		enqueue.assert_called_once_with(enqueue_after_commit=False)
