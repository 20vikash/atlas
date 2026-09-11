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

frappe.ui.form.on("Virtual Machine Image", {
	refresh(frm) {
		if (frm.doc.image_type === "machine" && frm.doc.status === "Failed") {
			frm.add_custom_button(__("Retry Transfer"), () => {
				frm.call("retry_transfer").then(() => frm.reload_doc());
			});
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
