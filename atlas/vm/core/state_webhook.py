from __future__ import annotations

import frappe

STATE_DOCTYPE = "Virtual Machine State"
DEFAULT_CENTRAL_ID = 1
REQUEST_TIMEOUT_SECONDS = 10
MAXIMUM_RETRIES = 3

DELIVERIES = {
	"on_update": "vm.state",
	"on_trash": "vm.state.deleted",
}


def configure_state_webhooks(
	request_url: str,
	webhook_secret: str,
	central_id: int = DEFAULT_CENTRAL_ID,
	enabled: bool = True,
) -> list[str]:
	"""Point the Virtual Machine State deliveries of one Central at request_url.

	More than one central_id registers only in development.
	"""
	region_name = frappe.get_cached_value("Atlas Settings", "Atlas Settings", "region_name")

	return [
		_upsert_webhook(document_event, request_url, webhook_secret, central_id, enabled, region_name)
		for document_event in DELIVERIES
	]


def _upsert_webhook(
	document_event: str,
	request_url: str,
	webhook_secret: str,
	central_id: int,
	enabled: bool,
	region_name: str | None,
) -> str:
	"""Create or refresh the Webhook for one state event."""
	name = webhook_name(document_event, central_id)
	if frappe.db.exists("Webhook", name):
		webhook = frappe.get_doc("Webhook", name)
	else:
		webhook = frappe.new_doc("Webhook")

	webhook.name = name
	webhook.webhook_doctype = STATE_DOCTYPE
	webhook.webhook_docevent = document_event
	webhook.condition = None
	webhook.request_url = request_url
	webhook.is_dynamic_url = 0
	webhook.background_jobs_queue = None
	webhook.request_method = "POST"
	webhook.request_structure = "JSON"
	webhook.webhook_json = build_webhook_json(document_event)
	webhook.enable_security = 1
	webhook.webhook_secret = webhook_secret
	webhook.timeout = REQUEST_TIMEOUT_SECONDS
	webhook.max_retries = MAXIMUM_RETRIES
	webhook.enabled = int(enabled)
	webhook.set("webhook_headers", build_webhook_headers(region_name))
	webhook.save(ignore_permissions=True)

	return name


def webhook_name(document_event: str, central_id: int) -> str:
	"""Return the Webhook name for one state event."""
	label = document_event.replace("_", " ").title()
	return f"{STATE_DOCTYPE} - {label} - Central - {central_id}"


def build_webhook_json(document_event: str) -> str:
	"""Return the delivered body for one state event."""
	return frappe.as_json(
		{
			"event": DELIVERIES[document_event],
			"virtual_machine": "{{ doc.virtual_machine }}",
			"status": "{{ doc.status }}",
			"observed_at": "{{ doc.synced_at }}",
		}
	)


def build_webhook_headers(region_name: str | None) -> list[dict[str, str]]:
	"""Return the headers of every state delivery."""
	headers = [{"key": "Content-Type", "value": "application/json"}]
	if region_name:
		headers.append({"key": "X-Atlas-Region", "value": region_name})

	return headers
