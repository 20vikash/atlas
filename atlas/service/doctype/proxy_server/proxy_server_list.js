function showCreateProxyServerDialog() {
	const dialog = new frappe.ui.Dialog({
		title: __("Create Proxy Server"),
		fields: [
			{
				fieldname: "virtual_machine_image",
				fieldtype: "Link",
				label: __("Virtual Machine Image"),
				options: "Virtual Machine Image",
				reqd: 1,
				filters: { enabled: 1, status: "Available" },
			},
			{ fieldname: "vcpus", fieldtype: "Int", label: __("vCPUs"), reqd: 1, default: 2 },
			{
				fieldname: "memory_mib",
				fieldtype: "Int",
				label: __("Memory (MiB)"),
				reqd: 1,
				default: 4096,
			},
			{
				fieldname: "disk_mib",
				fieldtype: "Int",
				label: __("Disk (MiB)"),
				reqd: 1,
				default: 16384,
			},
		],
		primary_action_label: __("Create"),
		primary_action(values) {
			frappe.call({
				method: "atlas.service.doctype.proxy_server.proxy_server.create",
				args: { request: values },
				freeze: true,
				freeze_message: __("Creating Proxy Server..."),
				callback(response) {
					dialog.hide();
					if (response.message.is_draft) {
						frappe.show_alert({
							message: __(
								"Metal did not confirm the VM request. Atlas kept the Proxy Server."
							),
							indicator: "orange",
						});
					}
					frappe.set_route("Form", "Proxy Server", response.message.name);
				},
			});
		},
	});
	dialog.show();
}

frappe.listview_settings["Proxy Server"] = {
	refresh(listview) {
		listview.page.clear_primary_action();
		if (!has_common(frappe.user_roles, ["System Manager"])) return;
		listview.page.add_inner_button(__("Create Proxy Server"), showCreateProxyServerDialog);
	},
};
