from __future__ import annotations

import hashlib
from pathlib import Path

import click
import frappe
from frappe.commands import pass_context
from frappe.exceptions import SiteNotSpecifiedError
from frappe.utils.bench_helper import CliCtxObj

from atlas.atlas.core.host_binaries import (
	HostBinary,
	ensure_build_environment,
	ensure_go_toolchain,
	find_host_binary,
	is_published,
	publish_host_binary,
	source_digest,
)
from atlas.atlas.object_storage import ObjectStorageError
from atlas.service.core import http_proxy_package
from atlas.vm.core.image_builder import build_ubuntu_image, publish_ubuntu_image


@click.command("build-metald")
@pass_context
def build_metald(context: CliCtxObj) -> None:
	"""Build metald and link it in Atlas Settings."""
	build_and_publish(context, find_host_binary("metald"))


@click.command("build-wg-mesh")
@pass_context
def build_wg_mesh(context: CliCtxObj) -> None:
	"""Build the Atlas WG Mesh CLI and link it in Atlas Settings."""
	build_and_publish(context, find_host_binary("wg-mesh"))


def build_and_publish(context: CliCtxObj, binary: HostBinary) -> None:
	"""Build one host binary for each site, unless its sources are unchanged."""
	if not context.sites:
		raise SiteNotSpecifiedError

	for site in context.sites:
		try:
			frappe.init(site)
			frappe.connect()

			digest = source_digest(binary)
			if is_published(binary, digest):
				click.echo(f"{binary.label} is current on {site}")
				continue

			ensure_build_environment((binary,))
			click.echo(f"Building {binary.label} for {site}")
			file_name = publish_host_binary(binary, ensure_go_toolchain(), digest)
			frappe.db.commit()  # nosemgrep
			click.echo(f"Published {binary.label} as File {file_name} on {site}")
		finally:
			frappe.destroy()


@click.command("build-http-proxy-package")
@pass_context
def build_http_proxy_package(context: CliCtxObj) -> None:
	"""Package the HTTP proxy component and link it in Atlas Settings."""
	if not context.sites:
		raise SiteNotSpecifiedError

	for site in context.sites:
		try:
			frappe.init(site)
			frappe.connect()

			archive = http_proxy_package.build_archive()
			digest = hashlib.sha256(archive).hexdigest()
			if http_proxy_package.is_published(digest):
				click.echo(f"{http_proxy_package.PACKAGE_LABEL} is current on {site}")
				continue

			click.echo(f"Packaging the HTTP proxy for {site}")
			file_name = http_proxy_package.publish_package(archive, digest)
			frappe.db.commit()  # nosemgrep
			click.echo(f"Published {http_proxy_package.PACKAGE_LABEL} as File {file_name} on {site}")
		finally:
			frappe.destroy()


@click.command("build-ubuntu-base-image")
@click.option("--version", type=click.Choice(["22.04", "24.04"]), required=True)
@click.option("--architecture", type=click.Choice(["amd64"]), default="amd64", show_default=True)
@click.option("--minimal", is_flag=True, help="Build the Ubuntu minimal cloud image.")
@click.option("--title")
@click.option(
	"--storage",
	type=click.Choice(["object-storage", "site-file"]),
	default="object-storage",
	show_default=True,
	help="Store the artifacts in object storage, or as public site files during bootstrap.",
)
@click.option(
	"--output-directory", type=click.Path(path_type=Path), default=Path("./dist"), show_default=True
)
@pass_context
def build_ubuntu_base_image(
	context: CliCtxObj,
	version: str,
	architecture: str,
	minimal: bool,
	title: str | None,
	storage: str,
	output_directory: Path,
) -> None:
	"""Build and publish a public Ubuntu server cloud image."""
	if not context.sites:
		raise SiteNotSpecifiedError
	if minimal and version != "24.04":
		raise click.UsageError("minimal images are available only for Ubuntu 24.04")

	title = title or f"ubuntu-{version}" + ("-minimal" if minimal else "")
	click.echo(f"Building {title} for {architecture}")
	image_path, kernel_path = build_ubuntu_image(version, architecture, minimal, output_directory)
	for site in context.sites:
		try:
			frappe.init(site)
			frappe.connect()
			click.echo(f"Publishing files to {site}")
			try:
				publish_ubuntu_image(
					title,
					version,
					architecture,
					image_path,
					kernel_path,
					"Site File" if storage == "site-file" else "Object Storage",
				)
			except ObjectStorageError as error:
				raise click.UsageError(str(error)) from error
			frappe.db.commit()  # nosemgrep
			click.echo(f"Created Virtual Machine Image for {site}")
		finally:
			frappe.destroy()


commands = [build_metald, build_wg_mesh, build_http_proxy_package, build_ubuntu_base_image]
