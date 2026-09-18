from __future__ import annotations

import logging
from typing import Any

import boto3
from botocore.config import Config
from botocore.exceptions import BotoCoreError, ClientError

from atlas.atlas.core.server_providers.base import ProviderOperationError

logger = logging.getLogger("atlas.provider.aws")

RETRYABLE_ERROR_CODES = frozenset(
	{
		"RequestLimitExceeded",
		"Throttling",
		"ThrottlingException",
		"InsufficientInstanceCapacity",
		"Unavailable",
		"InternalError",
		"InternalFailure",
		"ServiceUnavailable",
	}
)


class AwsError(ProviderOperationError):
	"""Raised when AWS rejects a provider request."""


class AwsClient:
	"""Send authenticated requests to the AWS API."""

	def __init__(self, access_key_id: str, secret_access_key: str, region: str) -> None:
		self.session = boto3.session.Session(
			aws_access_key_id=access_key_id,
			aws_secret_access_key=secret_access_key,
			region_name=region,
		)
		self.configuration = Config(retries={"max_attempts": 3, "mode": "standard"})
		self._clients: dict[str, Any] = {}

	def call(
		self, service: str, operation: str, *, allow_missing: bool = False, **parameters: object
	) -> dict[str, Any]:
		"""Return the response for one AWS API operation."""
		logger.info(
			"Provider request started",
			extra={"provider": "AWS", "operation": operation, "resource": service},
		)
		try:
			method = getattr(self.client(service), operation)
			return method(**parameters)
		except ClientError as error:
			code = error.response.get("Error", {}).get("Code", "")
			if allow_missing and self.is_missing_code(code):
				return {}
			logger.warning(
				"Provider request failed",
				extra={"provider": "AWS", "operation": operation, "resource": service, "code": code},
			)
			raise AwsError(
				f"{service}.{operation} failed with {code or 'an unknown error'}",
				code="provider_api_error",
				is_retryable=code in RETRYABLE_ERROR_CODES,
			) from error
		except BotoCoreError as error:
			logger.warning(
				"Provider request failed",
				extra={"provider": "AWS", "operation": operation, "resource": service},
			)
			raise AwsError(
				f"{service}.{operation} could not reach AWS",
				code="provider_transport_error",
				is_retryable=True,
			) from error

	def paginate(self, service: str, operation: str, key: str, **parameters: object) -> list[Any]:
		"""Return every item under one key across all response pages."""
		items: list[Any] = []
		while True:
			response = self.call(service, operation, **parameters)
			page = response.get(key, [])
			if not isinstance(page, list):
				raise AwsError(f"{service}.{operation} returned an invalid {key} list")
			items.extend(page)
			next_token = response.get("NextToken")
			if not next_token:
				return items
			if not isinstance(next_token, str):
				raise AwsError(f"{service}.{operation} returned an invalid pagination token")
			parameters["NextToken"] = next_token

	def client(self, service: str) -> Any:
		"""Return the cached boto3 client for one service."""
		if service not in self._clients:
			self._clients[service] = self.session.client(service, config=self.configuration)
		return self._clients[service]

	@staticmethod
	def is_missing_code(code: str) -> bool:
		"""Report whether an AWS error code means the resource does not exist."""
		return code.endswith(".NotFound") or code.endswith("NotFoundException")
