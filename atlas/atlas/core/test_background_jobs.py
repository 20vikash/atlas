import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.background_jobs import run_as_admin


@run_as_admin
def read_job_user() -> str:
	return frappe.session.user


class TestAdministratorJob(UnitTestCase):
	def test_job_runs_as_administrator_and_restores_the_user(self) -> None:
		previous_user = frappe.session.user
		frappe.set_user("Guest")
		try:
			self.assertEqual(read_job_user(), "Administrator")
			self.assertEqual(frappe.session.user, "Guest")
		finally:
			frappe.set_user(previous_user)

	def test_job_refuses_to_run_during_a_request(self) -> None:
		"""A queued job must not raise the privileges of a request."""
		frappe.local.request = frappe._dict(path="/api/atlas/images")
		try:
			with self.assertRaises(RuntimeError):
				read_job_user()
		finally:
			frappe.local.request = None
