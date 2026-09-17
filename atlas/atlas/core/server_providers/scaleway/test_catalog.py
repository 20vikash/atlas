from __future__ import annotations

import json
from types import SimpleNamespace

from frappe.tests import UnitTestCase

from atlas.atlas.core.server_providers.scaleway.catalog import ScalewayCatalog
from atlas.atlas.core.server_providers.scaleway.client import ScalewayError


class TestScalewayCatalog(UnitTestCase):
	def test_size_architecture_comes_from_cpu_names(self) -> None:
		for cpu_name, architecture in (
			("AMD EPYC 8434P", "amd64"),
			("Intel Xeon Gold", "amd64"),
			("Ampere Altra", "arm64"),
		):
			with self.subTest(cpu_name=cpu_name):
				sizes = ScalewayCatalog().get_server_sizes(
					[
						{
							"name": "size",
							"subscription_period": "hourly",
							"cpus": [{"name": cpu_name, "core_count": 4}],
						}
					]
				)
				self.assertEqual(sizes[0].architecture, architecture)

	def test_size_architecture_rejects_unknown_or_mixed_cpus(self) -> None:
		for cpu_names in ([], ["Unknown"], ["AMD EPYC", "Ampere Altra"]):
			with self.subTest(cpu_names=cpu_names), self.assertRaises(ScalewayError):
				ScalewayCatalog().get_server_sizes(
					[
						{
							"name": "size",
							"subscription_period": "hourly",
							"cpus": [{"name": name, "core_count": 4} for name in cpu_names],
						}
					]
				)

	def test_size_architecture_rejects_conflicting_offers(self) -> None:
		with self.assertRaises(ScalewayError):
			ScalewayCatalog().get_server_sizes(
				[
					{
						"name": "size",
						"subscription_period": "hourly",
						"cpus": [{"name": "AMD EPYC", "core_count": 4}],
					},
					{
						"name": "size",
						"subscription_period": "monthly",
						"cpus": [{"name": "Ampere Altra", "core_count": 4}],
					},
				]
			)

	def test_get_server_images_keeps_supported_os_versions(self) -> None:
		images = ScalewayCatalog().get_server_images([{"name": "Ubuntu", "version": "24.04 LTS"}])

		self.assertEqual(images[0].os, "Ubuntu")
		self.assertEqual(images[0].version, "24.04")

	def test_get_server_images_skips_unsupported_os_versions(self) -> None:
		images = ScalewayCatalog().get_server_images([{"name": "Ubuntu", "version": "13"}])

		self.assertEqual(images, ())

	def test_get_offer_id_uses_the_subscription_period(self) -> None:
		size = SimpleNamespace(
			name="Scaleway/EM-A410X",
			provider_metadata=json.dumps({"hourly": {"id": "hourly-id", "monthly_offer_id": "monthly-id"}}),
		)

		offer_id = ScalewayCatalog().get_offer_id(size, "monthly")

		self.assertEqual(offer_id, "monthly-id")

	def test_get_private_network_option_id_uses_offer_metadata(self) -> None:
		size = SimpleNamespace(
			name="Scaleway/EM-A410X",
			provider_metadata=json.dumps(
				{"hourly": {"options": [{"id": "private-network-id", "private_network": {}}]}}
			),
		)

		option_id = ScalewayCatalog().get_private_network_option_id(size, "hourly")

		self.assertEqual(option_id, "private-network-id")

	def test_get_private_network_option_id_matches_the_subscription_period(self) -> None:
		size = SimpleNamespace(
			name="Scaleway/EM-A410X",
			provider_metadata=json.dumps(
				{
					"hourly": {"options": [{"id": "hourly-option", "private_network": {}}]},
					"monthly": {"options": [{"id": "monthly-option", "private_network": {}}]},
				}
			),
		)

		option_id = ScalewayCatalog().get_private_network_option_id(size, "monthly")

		self.assertEqual(option_id, "monthly-option")

	def test_get_private_network_option_id_rejects_a_missing_period(self) -> None:
		"""Sending an hourly option with a monthly offer makes Scaleway fail."""
		size = SimpleNamespace(
			name="Scaleway/EM-A410X",
			provider_metadata=json.dumps(
				{"hourly": {"options": [{"id": "hourly-option", "private_network": {}}]}}
			),
		)

		with self.assertRaises(ScalewayError):
			ScalewayCatalog().get_private_network_option_id(size, "monthly")
