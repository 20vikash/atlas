from unittest.mock import patch

import frappe
from frappe.tests import IntegrationTestCase, UnitTestCase
from frappe.utils import add_to_date, get_datetime, now_datetime

from atlas.atlas.core.install import complete_setup_wizard, realign_scheduled_job_baselines


class TestSetupWizard(UnitTestCase):
	def test_the_wizard_is_walked_with_no_answers(self) -> None:
		with (
			patch("frappe.desk.page.setup_wizard.setup_wizard.setup_complete") as setup_complete,
			patch("atlas.atlas.core.install.frappe.get_system_settings", return_value="UTC"),
			patch("atlas.atlas.core.install.frappe.db.set_single_value") as set_single_value,
		):
			complete_setup_wizard()

		setup_complete.assert_called_once_with({})
		set_single_value.assert_not_called()

	def test_a_site_without_a_time_zone_gets_utc(self) -> None:
		with (
			patch("frappe.desk.page.setup_wizard.setup_wizard.setup_complete"),
			patch("atlas.atlas.core.install.frappe.get_system_settings", return_value=None),
			patch("atlas.atlas.core.install.frappe.db.set_single_value") as set_single_value,
		):
			complete_setup_wizard()

		set_single_value.assert_called_once_with("System Settings", "time_zone", "UTC")

	def test_a_chosen_time_zone_is_kept(self) -> None:
		with (
			patch("frappe.desk.page.setup_wizard.setup_wizard.setup_complete"),
			patch("atlas.atlas.core.install.frappe.get_system_settings", return_value="Europe/Berlin"),
			patch("atlas.atlas.core.install.frappe.db.set_single_value") as set_single_value,
		):
			complete_setup_wizard()

		set_single_value.assert_not_called()


class TestScheduledJobBaselines(IntegrationTestCase):
	def setUp(self) -> None:
		self.job = frappe.get_doc(
			doctype="Scheduled Job Type",
			method="atlas.tests.realign_probe",
			frequency="Hourly",
		).insert(ignore_permissions=True)
		self.addCleanup(self.job.delete)

	def baseline(self):
		job = frappe.get_doc("Scheduled Job Type", self.job.name)
		return job.last_execution or job.creation

	def test_a_baseline_ahead_of_the_clock_is_brought_back(self) -> None:
		self.job.db_set("last_execution", add_to_date(now_datetime(), hours=6), update_modified=False)

		realign_scheduled_job_baselines()

		self.assertLessEqual(get_datetime(self.baseline()), now_datetime())

	def test_a_cold_job_stamped_in_the_future_becomes_reachable(self) -> None:
		self.job.db_set("creation", add_to_date(now_datetime(), hours=6), update_modified=False)
		self.job.db_set("last_execution", None, update_modified=False)
		unreachable = frappe.get_doc("Scheduled Job Type", self.job.name).get_next_execution()

		realign_scheduled_job_baselines()

		self.assertGreater(unreachable, add_to_date(now_datetime(), hours=5))
		self.assertLessEqual(
			frappe.get_doc("Scheduled Job Type", self.job.name).get_next_execution(),
			add_to_date(now_datetime(), hours=1),
		)

	def test_a_healthy_job_is_left_alone(self) -> None:
		last_execution = add_to_date(now_datetime(), hours=-2)
		self.job.db_set("last_execution", last_execution, update_modified=False)

		realign_scheduled_job_baselines()

		self.assertEqual(get_datetime(self.baseline()), last_execution)
