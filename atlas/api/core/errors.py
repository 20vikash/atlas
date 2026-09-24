from typing import Any

import frappe
from pydantic import BaseModel, Field
from pydantic import ValidationError as PydanticValidationError

from atlas.atlas.core.exceptions import AtlasUserError

CODE_BY_STATUS = {400: "invalid_request", 404: "not_found", 409: "conflict", 503: "out_of_capacity"}
MESSAGE_BY_STATUS = {
	400: "The request is not valid.",
	404: "The resource does not exist.",
	409: "The resource state does not allow this request.",
	503: "No host capacity is available. Retry later.",
}


class ApiErrorField(BaseModel):
	"""One invalid field named in an Atlas API error."""

	name: str = Field(description="Request field path.")
	message: str = Field(description="Reason that the field is not valid.")


class ApiErrorDetail(BaseModel):
	"""The stable error details returned by Atlas."""

	code: str = Field(description="Stable machine-readable error code.")
	message: str = Field(description="Safe description of the failure.")
	fields: list[ApiErrorField] = Field(description="Invalid request fields, or an empty list.")


class ApiErrorResponse(BaseModel):
	"""The JSON body of an Atlas API error."""

	error: ApiErrorDetail = Field(description="Error details.")


class ApiError(Exception):
	"""One API failure with a stable code, a safe message, and field details."""

	code = "internal_error"
	http_status_code = 500

	def __init__(
		self,
		message: str,
		*,
		code: str | None = None,
		status: int | None = None,
		fields: list[dict[str, str]] | None = None,
		headers: dict[str, str] | None = None,
	) -> None:
		super().__init__(message)
		self.message = message
		self.fields = list(fields or ())
		self.headers = dict(headers or {})
		if code:
			self.code = code
		if status:
			self.http_status_code = status

	def as_dict(self) -> dict[str, Any]:
		"""Return the JSON body for this failure."""
		return {"error": {"code": self.code, "message": self.message, "fields": self.fields}}


class InvalidRequest(ApiError):
	"""The caller sent data that the route cannot accept."""

	code = "invalid_request"
	http_status_code = 400


class AuthenticationRequired(ApiError):
	"""The caller has no Frappe session."""

	code = "authentication_required"
	http_status_code = 401


class PermissionDenied(ApiError):
	"""The session cannot use this route."""

	code = "permission_denied"
	http_status_code = 403


class ResourceNotFound(ApiError):
	"""The resource is absent, or it belongs to another tenant."""

	code = "not_found"
	http_status_code = 404


class ResourceConflict(ApiError):
	"""The resource state does not allow this request."""

	code = "conflict"
	http_status_code = 409


def describe_exception(exception: Exception) -> tuple[int, dict[str, Any]]:
	"""Return the status code and the JSON error body for one exception."""
	error = as_api_error(exception)
	log_unexpected_error(error, exception)
	return error.http_status_code, error.as_dict()


def log_unexpected_error(error: ApiError, exception: Exception) -> None:
	"""Log unexpected server failures, but not expected 503 responses."""
	if error.http_status_code < 500 or error.http_status_code == 503:
		return
	frappe.log_error(
		title=f"Unhandled API error: {type(exception).__name__}",
		message=frappe.get_traceback(),
		defer_insert=True,
	)


def as_api_error(exception: Exception) -> ApiError:
	"""Convert any exception into the API failure that the caller may read."""
	if isinstance(exception, ApiError):
		return exception
	if isinstance(exception, AtlasUserError):
		return as_status_error(exception, str(exception))
	if isinstance(exception, PydanticValidationError):
		return InvalidRequest(get_validation_message(exception), fields=get_validation_fields(exception))
	if isinstance(exception, frappe.AuthenticationError | frappe.SessionExpired):
		return AuthenticationRequired("Authentication is required.")
	if isinstance(exception, frappe.PermissionError):
		return PermissionDenied("You cannot use this resource.")
	if isinstance(exception, frappe.DoesNotExistError):
		return ResourceNotFound("The resource does not exist.")

	status = getattr(exception, "http_status_code", 500)
	if status >= 500:
		return ApiError("The request could not be completed.", status=status)

	return as_status_error(exception)


def as_status_error(exception: Exception, message: str | None = None) -> ApiError:
	"""Return a supported status error, preserving an explicit error code."""
	status = getattr(exception, "http_status_code", 400)
	status = status if status in CODE_BY_STATUS else 400
	code = getattr(exception, "code", None) or CODE_BY_STATUS[status]
	return ApiError(
		message or MESSAGE_BY_STATUS[status],
		code=code,
		status=status,
		headers=dict(getattr(exception, "headers", {}) or {}),
	)


def get_validation_message(exception: PydanticValidationError) -> str:
	"""Return the first request-level reason, which names no field."""
	for error in exception.errors():
		if not error["loc"] and "error" in error.get("ctx", {}):
			return str(error["ctx"]["error"])
	return "The request is not valid."


def get_validation_fields(exception: PydanticValidationError) -> list[dict[str, str]]:
	"""Return one entry for each field that failed validation."""
	return [
		{"name": ".".join(str(part) for part in error["loc"]), "message": error["msg"]}
		for error in exception.errors()
		if error["loc"]
	]
