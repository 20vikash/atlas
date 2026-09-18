from unittest.mock import patch

import frappe
from frappe.tests import UnitTestCase
from pydantic import BaseModel
from pydantic import ValidationError as PydanticValidationError

from atlas.api.core.errors import (
	InvalidRequest,
	ResourceConflict,
	ResourceNotFound,
	describe_exception,
)
from atlas.api.models import CapacityPendingResponse
from atlas.api.routes.virtual_machines import create_virtual_machine
from atlas.vm.core.placement.context import CapacityPending


class Machine(BaseModel):
	name: str


class HostFailure(frappe.ValidationError):
	http_status_code = 502


class TestErrorBody(UnitTestCase):
	def test_api_error_keeps_its_code_and_fields(self) -> None:
		status, body = describe_exception(
			InvalidRequest("bad", fields=[{"name": "cpu_millicores", "message": "too small"}])
		)

		self.assertEqual(status, 400)
		self.assertEqual(
			body,
			{
				"error": {
					"code": "invalid_request",
					"message": "bad",
					"fields": [{"name": "cpu_millicores", "message": "too small"}],
				}
			},
		)

	def test_validation_error_lists_every_failing_field(self) -> None:
		try:
			Machine(**{})
		except PydanticValidationError as error:
			status, body = describe_exception(error)

		self.assertEqual(status, 400)
		self.assertEqual([field["name"] for field in body["error"]["fields"]], ["name"])

	def test_frappe_failures_map_to_api_codes(self) -> None:
		cases = (
			(frappe.AuthenticationError(), 401, "authentication_required"),
			(frappe.PermissionError(), 403, "permission_denied"),
			(frappe.DoesNotExistError(), 404, "not_found"),
			(frappe.ValidationError("Disk size can only increase."), 400, "invalid_request"),
		)
		for exception, expected_status, expected_code in cases:
			status, body = describe_exception(exception)
			self.assertEqual(status, expected_status)
			self.assertEqual(body["error"]["code"], expected_code)

	def test_conflict_keeps_its_status(self) -> None:
		status, body = describe_exception(ResourceConflict("in use"))

		self.assertEqual(status, 409)
		self.assertEqual(body["error"]["code"], "conflict")

	def test_host_failure_hides_the_internal_message(self) -> None:
		status, body = describe_exception(HostFailure("Metal request failed: Server node-1 is down"))

		self.assertEqual(status, 502)
		self.assertEqual(body["error"]["message"], "The request could not be completed.")

	def test_unknown_failure_hides_the_internal_message(self) -> None:
		status, body = describe_exception(RuntimeError("object key images/secret/rootfs.img"))

		self.assertEqual(status, 500)
		self.assertEqual(body["error"]["message"], "The request could not be completed.")

	def test_validation_error_hides_the_internal_message(self) -> None:
		status, body = describe_exception(frappe.ValidationError("object key images/secret/rootfs.img"))

		self.assertEqual(status, 400)
		self.assertEqual(body["error"]["message"], "The request is not valid.")

	def test_not_found_reports_a_missing_resource(self) -> None:
		status, _ = describe_exception(ResourceNotFound("gone"))
		self.assertEqual(status, 404)

	def test_client_errors_do_not_create_error_logs(self) -> None:
		with patch("atlas.api.core.base.frappe.log_error") as log_error:
			describe_exception(frappe.PermissionError())

		log_error.assert_not_called()

	def test_pending_capacity_is_retryable_and_visible(self) -> None:
		with patch("atlas.api.core.errors.frappe.log_error") as log_error:
			response = create_virtual_machine.build_error_response(CapacityPending("Host node-a is starting"))

		self.assertEqual(response.status_code, 503)
		self.assertEqual(response.json["error"]["code"], "capacity_pending")
		self.assertIn("node-a", response.json["error"]["message"])
		self.assertEqual(response.headers["Retry-After"], "15")
		self.assertEqual(CapacityPendingResponse.model_validate(response.json).error.code, "capacity_pending")
		log_error.assert_not_called()

	def test_null_error_body_does_not_add_retry_header(self) -> None:
		with patch("atlas.api.core.base.describe_exception", return_value=(400, {"error": None})):
			response = create_virtual_machine.build_error_response(ValueError("bad"))

		self.assertEqual(response.status_code, 400)
		self.assertNotIn("Retry-After", response.headers)
