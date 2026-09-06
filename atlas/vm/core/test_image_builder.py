from pathlib import Path
from tempfile import TemporaryDirectory
from types import SimpleNamespace
from unittest.mock import call, patch

from frappe.tests import UnitTestCase

from atlas.vm.core.image_builder import build_ubuntu_image, publish_ubuntu_image


class TestUbuntuImageBuilder(UnitTestCase):
	def test_build_uses_the_virtual_machine_image_script_and_complete_names(self) -> None:
		with (
			TemporaryDirectory() as temporary_directory,
			patch("atlas.vm.core.image_builder.os.geteuid", return_value=1000),
			patch("atlas.vm.core.image_builder.subprocess.run") as run,
		):
			image_path, kernel_path = build_ubuntu_image("24.04", "amd64", True, Path(temporary_directory))

		command = run.call_args.args[0]
		self.assertEqual(command[0], "sudo")
		self.assertTrue(str(command[1]).endswith("vm/scripts/build_ubuntu_server_image.sh"))
		self.assertIn("--minimal", command)
		self.assertEqual(image_path.name, "ubuntu-24.04-minimal-amd64.ext4")
		self.assertEqual(kernel_path.name, "vmlinux-ubuntu-24.04-minimal-server")
		run.assert_called_once_with(command, check=True)

	def test_publish_keeps_an_unchanged_image_record(self) -> None:
		existing = SimpleNamespace(image_sha256="a" * 64, kernel_sha256="b" * 64)
		with (
			patch("atlas.vm.core.image_builder.get_sha256", side_effect=["a" * 64, "b" * 64]) as sha256,
			patch("atlas.vm.core.image_builder.frappe.db.exists", return_value="image-1"),
			patch("atlas.vm.core.image_builder.frappe.get_doc", return_value=existing),
			patch("atlas.vm.core.image_builder.frappe.get_single") as get_single,
		):
			publish_ubuntu_image(
				"Ubuntu 24.04",
				"24.04",
				"amd64",
				Path("rootfs.img"),
				Path("kernel"),
			)

		self.assertEqual(sha256.call_args_list, [call(Path("rootfs.img")), call(Path("kernel"))])
		get_single.assert_not_called()
