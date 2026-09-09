from __future__ import annotations

from typing import TYPE_CHECKING

import frappe

from atlas.api.core.base import (
	ApiResult,
	ListQuery,
	Page,
	build_page,
	get_owned_document,
)
from atlas.api.core.docs import api_docs
from atlas.api.core.errors import (
	ResourceConflict,
	ResourceNotFound,
)
from atlas.api.models import ImageDownloadQuery, ImageDownloadResponse, ImageResponse
from atlas.api.router import images
from atlas.auth.tenant import get_tenant_id

if TYPE_CHECKING:
	from atlas.vm.doctype.virtual_machine_image.virtual_machine_image import VirtualMachineImage


def get_owned_image(image_id: str) -> VirtualMachineImage:
	"""Return one tenant image or a shared System image."""
	image: VirtualMachineImage = get_owned_document("Virtual Machine Image", image_id, {})
	if not image.is_visible_to_tenant(get_tenant_id()):
		raise ResourceNotFound("The Virtual Machine Image does not exist.")
	return image


@images.get("")
@api_docs()
def list_images(query: ListQuery) -> Page[ImageResponse]:
	"""List images.

	Returns one page of System and Machine images owned by the tenant in newest-first order.
	"""
	rows: list[VirtualMachineImage] = frappe.get_all(
		"Virtual Machine Image",
		or_filters={"tenant_id": get_tenant_id(), "image_type": "System"},
		fields=[
			"name",
			"tenant_id",
			"title",
			"image_type",
			"platform",
			"operating_system",
			"operating_system_version",
			"status",
			"enabled",
			"supports_cloud_init",
			"cache_image",
			"memory_snapshot",
			"image_size_mib",
			"kernel_size_mib",
			"transfer_progress",
			"transfer_error",
			"creation",
		],
		order_by="creation desc",
		start=query.offset,
		limit=query.fetch_limit,
	)
	return build_page([ImageResponse.from_document(row) for row in rows], query)


@images.get("<image_id>")
@api_docs()
def get_image(image_id: str) -> ImageResponse:
	"""Get image.

	Returns one tenant image with its artifact metadata and transfer state.
	"""
	return ImageResponse.from_document(get_owned_image(image_id))


@images.get("<image_id>/download")
@api_docs(
	responses={200: {"description": "A signed artifact URL that expires after 24 hours."}},
)
def download_image(image_id: str, query: ImageDownloadQuery) -> ApiResult[ImageDownloadResponse]:
	"""Download image.

	Returns a signed download URL for the selected rootfs or kernel artifact. The response includes its size, SHA-256 value, and expiry time and cannot be cached.
	"""
	download = ImageDownloadResponse.from_download(
		get_owned_image(image_id).get_presigned_download_url(query.artifact)
	)
	return ApiResult(download, headers={"Cache-Control": "no-store"})


@images.delete("<image_id>")
@api_docs(
	responses={
		202: {"description": "Deletion started. Poll the image route."},
		409: {"description": "A virtual machine uses this image, or the image is not a Machine image."},
	},
)
def delete_image(image_id: str) -> ApiResult[ImageResponse]:
	"""Delete image.

	Starts deletion of an unused Available Machine image. A cleanup job removes its stored artifacts and remaining host snapshot data.
	"""
	image = get_owned_image(image_id)
	if image.is_shared:
		raise ResourceConflict("Only a Machine image can be deleted.")
	image.request_deletion()
	return ApiResult(ImageResponse.from_document(image), status=202)
