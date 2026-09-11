from unittest.mock import patch

from frappe.tests import UnitTestCase

from atlas.atlas.core.install import complete_setup_wizard


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
