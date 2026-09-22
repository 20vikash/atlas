function downloadArtifact(frm, artifact) {
	frm.call("get_presigned_download_url", { artifact }).then(({ message }) => {
		if (!message) {
			return;
		}

		// The signed URL is cross-origin, so the download attribute is advisory only.
		// Garage serves the artifact as an attachment.
		const link = document.createElement("a");
		link.href = message.url;
		link.download = "";
		document.body.appendChild(link);
		link.click();
		link.remove();
	});
}

function setTerminationProtection(frm) {
	const protecting = !frm.doc.is_termination_protected;
	const message = protecting
		? __("Protect {0}? Deletion is refused until you disable this.", [frm.doc.title.bold()])
		: __("Unprotect {0}? Anyone with write access can then delete it.", [
				frm.doc.title.bold(),
		  ]);

	frappe.confirm(message, () =>
		frm
			.call({
				method: "set_termination_protection",
				doc: frm.doc,
				args: { is_protected: protecting },
				freeze: true,
				freeze_message: __("Saving..."),
			})
			.then(() => frm.reload_doc())
	);
}

frappe.ui.form.on("Virtual Machine Image", {
	refresh(frm) {
		if (frm.doc.status === "Failed" && frm.doc.source_server) {
			frm.add_custom_button(__("Retry Transfer"), () => {
				frm.call("retry_transfer").then(() => frm.reload_doc());
			});
		}

		frm.add_custom_button(
			frm.doc.is_termination_protected
				? __("Disable Termination Protection")
				: __("Enable Termination Protection"),
			() => setTerminationProtection(frm),
			__("Dangerous Actions")
		);

		if (["Available", "Failed"].includes(frm.doc.status)) {
			frm.add_custom_button(
				__("Delete Image"),
				() => {
					frappe.confirm(
						__(
							"Delete {0}? New virtual machines cannot use it. Its artifacts stay while a virtual machine still needs them.",
							[frm.doc.title]
						),
						() => frm.call("request_deletion").then(() => frm.reload_doc())
					);
				},
				__("Dangerous Actions")
			);
		}

		if (frm.doc.artifact_storage === "Site File" && frm.doc.status === "Available") {
			frm.add_custom_button(__("Migrate to Object Storage"), () => {
				frm.call("migrate_to_object_storage").then(() => frm.reload_doc());
			});
		}

		// A Site File image links its artifacts on the form already.
		if (frm.doc.artifact_storage === "Object Storage" && frm.doc.status === "Available") {
			[
				[__("Image"), "rootfs"],
				[__("Kernel"), "kernel"],
			].forEach(([label, artifact]) => {
				frm.add_custom_button(
					label,
					() => downloadArtifact(frm, artifact),
					__("Download")
				);
			});
		}
	},
});
