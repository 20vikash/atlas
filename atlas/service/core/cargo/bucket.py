from __future__ import annotations

from datetime import timedelta
from typing import TYPE_CHECKING

import frappe
import requests
from frappe import _

from atlas.auth.issuer import issue_token

if TYPE_CHECKING:
	from atlas.atlas.doctype.atlas_settings.atlas_settings import AtlasSettings

CREATE_BUCKET_PATH = "/api/method/cargo.object_storage.api.bucket.create_bucket"
TOKEN_HEADER = "X-Cargo-Access-Token"
TOKEN_LIFETIME = timedelta(minutes=5)
REQUEST_TIMEOUT_SECONDS = 30
HEALTH_TIMEOUT_SECONDS = 5


class CargoBucket:
	"""Create the regional Atlas bucket on Cargo and store the key it returns once."""

	def __init__(self) -> None:
		self.settings: AtlasSettings = frappe.get_single("Atlas Settings")

	@property
	def name(self) -> str:
		"""Return the bucket Atlas owns in this region."""
		return f"atlas-{self.settings.region_name.lower()}"

	@property
	def endpoint_url(self) -> str:
		"""Return the S3 endpoint that Atlas uploads through."""
		return f"https://s3-svc.{self.settings.wildcard_domain}"

	@property
	def is_garage_ready(self) -> bool:
		"""Report whether Garage answers its health route through the Proxy."""
		url = f"https://s3-admin-svc.{self.settings.wildcard_domain}/health"
		try:
			return requests.get(url, timeout=HEALTH_TIMEOUT_SECONDS).ok
		except requests.RequestException:
			return False

	def provision(self) -> None:
		"""Create the bucket once Garage serves. Wait quietly until it does."""
		if self.settings.is_object_storage_configured:
			frappe.log_error(
				title="Atlas object storage is already configured",
				message=(
					f"Refused to replace bucket {self.settings.object_storage_bucket}. "
					"Clear the object storage fields in Atlas Settings to provision another bucket. "
					"Objects under the current bucket become unreachable when its key is replaced."
				),
			)
			return

		if not self.is_garage_ready:
			return

		self.store(self.create())

	def create(self) -> dict[str, str]:
		"""Ask Cargo for the bucket and the one key that opens it."""
		response = requests.post(
			f"https://cargo.{self.settings.wildcard_domain}{CREATE_BUCKET_PATH}",
			headers={TOKEN_HEADER: self.access_token()},
			json={"name": self.name, "region": self.settings.region_name},
			timeout=REQUEST_TIMEOUT_SECONDS,
		)
		if not response.ok:
			frappe.throw(
				_("Cargo refused the {0} bucket request with status {1}.").format(
					self.name, response.status_code
				)
			)

		return response.json()["message"]["credentials"]

	def access_token(self) -> str:
		"""Return a short-lived credential for the regional Cargo API."""
		return issue_token(
			self.settings,
			audience=self.settings.cargo_audience_id,
			subject="atlas",
			scope="*",
			tenant="0",
			lifetime=TOKEN_LIFETIME,
		)

	def store(self, credentials: dict[str, str]) -> None:
		"""Store the key at once. Cargo returns the secret here and keeps no copy."""
		self.settings.object_storage_bucket = self.name
		self.settings.object_storage_endpoint_url = self.endpoint_url
		self.settings.object_storage_region = self.settings.region_name
		self.settings.object_storage_access_key_id = credentials["access_key"]
		self.settings.object_storage_secret_access_key = credentials["secret_access_key"]
		self.settings.save(ignore_permissions=True)
		frappe.db.commit()  # nosemgrep


def enqueue_bucket_provisioning(enqueue_after_commit: bool = True) -> None:
	"""Queue Atlas bucket creation on the regional Cargo."""
	frappe.enqueue(
		"atlas.service.core.cargo.bucket.provision_bucket",
		queue="long",
		timeout=600,
		job_id="atlas||cargo-bucket||provision",
		deduplicate=True,
		enqueue_after_commit=enqueue_after_commit,
	)


def provision_bucket() -> None:
	CargoBucket().provision()


def enqueue_pending_bucket_provisioning() -> None:
	"""Retry bucket creation while Cargo serves and Atlas has no object storage."""
	if frappe.get_single("Atlas Settings").is_object_storage_configured:
		return

	if frappe.db.get_single_value("Cargo Server", "status") != "Active":
		return

	enqueue_bucket_provisioning(enqueue_after_commit=False)
