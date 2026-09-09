import inspect
import types
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, get_args, get_origin, get_type_hints

import orjson
from pydantic import BaseModel as PydanticBaseModel
from pydantic import TypeAdapter
from werkzeug.wrappers import Request

from atlas.api.core.errors import InvalidRequest


@dataclass(frozen=True)
class ParameterBinding:
	"""Converts a raw request value into the type annotated on a route parameter."""

	name: str
	annotation: Any
	adapter: TypeAdapter
	model: type[PydanticBaseModel] | None = None
	is_list: bool = False

	def convert(self, raw: Any) -> Any:
		"""Validate raw and return it as the annotated type."""
		return self.adapter.validate_python(raw)

	@property
	def empty_value(self) -> list | dict:
		"""Value to validate when the request carries no body."""
		return [] if self.is_list else {}


@dataclass(frozen=True)
class CallShape:
	"""The parameter names that a route function accepts."""

	positional: tuple[str, ...]
	keywords: frozenset[str]
	accepts_variable_keywords: bool

	@classmethod
	def of(cls, function: Callable) -> "CallShape":
		"""Read the call shape from the signature of function."""
		parameters = inspect.signature(function).parameters
		return cls(
			positional=tuple(
				name
				for name, parameter in parameters.items()
				if parameter.kind
				in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
			),
			keywords=frozenset(
				name
				for name, parameter in parameters.items()
				if parameter.kind in (inspect.Parameter.POSITIONAL_OR_KEYWORD, inspect.Parameter.KEYWORD_ONLY)
			),
			accepts_variable_keywords=any(
				parameter.kind is inspect.Parameter.VAR_KEYWORD for parameter in parameters.values()
			),
		)

	def is_supplied(self, name: str, args: tuple, kwargs: dict) -> bool:
		"""True when the caller already passed the named parameter."""
		return name in kwargs or name in self.positional[: len(args)]

	def accepted_keywords(self, kwargs: dict) -> dict:
		"""Drop the keywords that the function does not accept."""
		if self.accepts_variable_keywords:
			return kwargs

		return {name: value for name, value in kwargs.items() if name in self.keywords}


def resolve_binding(function: Callable, name: str) -> ParameterBinding | None:
	"""Return the binding for a named route parameter, or None when it is absent."""
	if name not in inspect.signature(function).parameters:
		return None

	annotation = get_type_hints(function).get(name)
	if annotation is None:
		raise TypeError(f"{function.__name__}: parameter '{name}' needs a type annotation")

	if get_origin(annotation) is list:
		item_type = next(iter(get_args(annotation)), None)
		model = as_model(item_type)
		if model is None:
			raise TypeError(f"{function.__name__}: parameter '{name}' must be a Pydantic model list")
		return build_binding(name, annotation, model, is_list=True)

	if get_origin(annotation) is not None or isinstance(annotation, types.UnionType):
		raise TypeError(f"{function.__name__}: parameter '{name}' cannot be a union or generic type")

	model = as_model(annotation)
	if model is None:
		raise TypeError(f"{function.__name__}: parameter '{name}' must be a Pydantic model")

	return build_binding(name, annotation, model, is_list=False)


def build_binding(
	name: str, annotation: Any, model: type[PydanticBaseModel] | None, is_list: bool
) -> ParameterBinding:
	"""Build a binding and its Pydantic adapter for one route parameter."""
	return ParameterBinding(
		name=name,
		annotation=annotation,
		adapter=TypeAdapter(annotation),
		model=model,
		is_list=is_list,
	)


def as_model(annotation: Any) -> type[PydanticBaseModel] | None:
	"""Return annotation when it is a Pydantic model, otherwise None."""
	if isinstance(annotation, type) and issubclass(annotation, PydanticBaseModel):
		return annotation

	return None


def read_json_body(request: Request, binding: ParameterBinding) -> Any:
	"""Return the decoded JSON body, or an empty container when the body is empty."""
	body = request.get_data(as_text=True)
	if not body:
		return binding.empty_value

	try:
		return orjson.loads(body)
	except orjson.JSONDecodeError as error:
		raise InvalidRequest("The request body is not valid JSON.") from error
