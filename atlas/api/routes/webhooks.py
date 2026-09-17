from __future__ import annotations

from atlas.api.core.docs import api_docs
from atlas.api.models import ConfigureWebhooksPayload, WebhookConfigurationResponse
from atlas.api.router import atlas_router
from atlas.vm.core.state_webhook import configure_state_webhooks


@atlas_router.put("webhooks", central_only=True)
@api_docs(
	request_example={
		"request_url": "https://central.example.com/api/method/central.api.state_delivery.receive",
		"webhook_secret": "a-shared-secret",
		"central_id": 1,
		"enabled": True,
	},
	responses={
		200: {"description": "The deliveries point at the receiver."},
		403: {"description": "The token does not act for every tenant."},
	},
)
def configure_webhooks(payload: ConfigureWebhooksPayload) -> WebhookConfigurationResponse:
	"""Configure webhooks.

	Point all Atlas event deliveries for one Central at its receiver. Atlas chooses the events and their content. A repeated call refreshes the configuration. Only a Central token, which carries tenant `*`, can use this route.
	"""
	names = configure_state_webhooks(
		request_url=str(payload.request_url),
		webhook_secret=payload.webhook_secret,
		central_id=payload.central_id,
		enabled=payload.enabled,
	)

	return WebhookConfigurationResponse(
		central_id=payload.central_id,
		enabled=payload.enabled,
		webhooks=names,
	)
