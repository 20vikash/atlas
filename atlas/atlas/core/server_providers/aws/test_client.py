from __future__ import annotations

from unittest.mock import Mock, patch

from botocore.exceptions import ClientError, EndpointConnectionError
from frappe.tests import UnitTestCase

from atlas.atlas.core.server_providers.aws.client import AwsClient, AwsError


class TestAwsClient(UnitTestCase):
	def test_throttling_is_retryable(self) -> None:
		client = self.client(error=self.client_error("Throttling"))

		with self.assertRaises(AwsError) as raised:
			client.call("ec2", "describe_instances")

		self.assertTrue(raised.exception.is_retryable)

	def test_an_invalid_request_is_not_retryable(self) -> None:
		client = self.client(error=self.client_error("InvalidParameterValue"))

		with self.assertRaises(AwsError) as raised:
			client.call("ec2", "describe_instances")

		self.assertFalse(raised.exception.is_retryable)

	def test_a_missing_resource_is_empty_when_allowed(self) -> None:
		client = self.client(error=self.client_error("InvalidInstanceID.NotFound"))

		self.assertEqual(client.call("ec2", "terminate_instances", allow_missing=True), {})

	def test_a_missing_resource_still_fails_when_not_allowed(self) -> None:
		client = self.client(error=self.client_error("InvalidInstanceID.NotFound"))

		with self.assertRaises(AwsError):
			client.call("ec2", "terminate_instances")

	def test_a_transport_failure_is_retryable(self) -> None:
		client = self.client(error=EndpointConnectionError(endpoint_url="https://ec2.example"))

		with self.assertRaises(AwsError) as raised:
			client.call("ec2", "describe_instances")

		self.assertTrue(raised.exception.is_retryable)

	def test_paginate_follows_every_page(self) -> None:
		client = self.client()
		client.call = Mock(
			side_effect=[
				{"Reservations": ["first"], "NextToken": "token"},
				{"Reservations": ["second"]},
			]
		)

		self.assertEqual(client.paginate("ec2", "describe_instances", "Reservations"), ["first", "second"])
		self.assertEqual(client.call.call_args.kwargs["NextToken"], "token")

	def test_paginate_rejects_an_invalid_item_list(self) -> None:
		client = self.client()
		client.call = Mock(return_value={"Reservations": "not-a-list"})

		with self.assertRaises(AwsError):
			client.paginate("ec2", "describe_instances", "Reservations")

	@staticmethod
	def client(*, error: Exception | None = None) -> AwsClient:
		with patch("atlas.atlas.core.server_providers.aws.client.boto3.session.Session"):
			client = AwsClient("access-key-id", "secret-access-key", "eu-west-1")
		operation = Mock(side_effect=error) if error else Mock(return_value={})
		client._clients["ec2"] = Mock(describe_instances=operation, terminate_instances=operation)
		return client

	@staticmethod
	def client_error(code: str) -> ClientError:
		return ClientError({"Error": {"Code": code, "Message": code}}, "DescribeInstances")
