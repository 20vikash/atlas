const UNOWNED_TENANT_ID = -1;

frappe.ui.form.on("Metal Server IP Address", {
	async onload(frm) {
		if (!frm.is_new()) return;

		const provider = await frappe.db.get_single_value("Atlas Settings", "server_provider");
		frm.is_manual_address = provider === "Generic";
		frm.toggle_display("provider_resource_id", !frm.is_manual_address);
	},

	address(frm) {
		if (frm.is_manual_address) {
			frm.set_value("provider_resource_id", frm.doc.address);
		}
	},

	async validate(frm) {
		if (!frm.is_new()) return;

		const is_generic_provider =
			frm.is_manual_address ??
			(await frappe.db.get_single_value("Atlas Settings", "server_provider")) === "Generic";
		if (!is_generic_provider) return;

		const is_confirmed = await new Promise((resolve) =>
			frappe.confirm(
				__(
					"Confirm that your provider routes this IPv4 address through VXLAN to every Metal Server. A virtual machine can then use it on any host."
				),
				() => resolve(true),
				() => resolve(false)
			)
		);
		if (!is_confirmed) {
			frappe.validated = false;
		}
	},

	refresh(frm) {
		const is_detached = frm.doc.status === "Allocated" && !frm.doc.virtual_machine;
		const is_owned = frm.doc.tenant_id !== UNOWNED_TENANT_ID;
		const is_ipv6 = frm.doc.version === "6";
		const is_system_manager = frappe.user.has_role("System Manager");

		[
			[
				__("Attach to Virtual Machine"),
				() => showAttachPrefixDialog(frm),
				is_ipv6 && frm.doc.status === "Allocated" && !frm.is_new() && is_system_manager,
				null,
				false,
			],
			[
				__("Detach from Virtual Machine"),
				() => frm.call("detach_public_ipv6").then(() => frm.reload_doc()),
				is_ipv6 && frm.doc.status === "Attached" && is_system_manager,
				__("Detach {0}/{1} from {2}?", [
					frm.doc.address.bold(),
					frm.doc.cidr,
					frm.doc.virtual_machine,
				]),
				true,
			],
			[
				__("Reserve"),
				() => frm.call("reserve").then(() => frm.reload_doc()),
				!is_ipv6 && is_owned && !frm.doc.reserved && frm.doc.status !== "Detaching",
				__("Keep {0} for tenant {1}? A detach no longer returns it to the shared pool.", [
					frm.doc.address.bold(),
					frm.doc.tenant_id,
				]),
				false,
			],
			[
				__("Reset Tenant"),
				() => frm.call("reset_tenant").then(() => frm.reload_doc()),
				!is_ipv6 && is_detached && is_owned && is_system_manager,
				__("Return {0} to the shared pool? Tenant {1} loses it.", [
					frm.doc.address.bold(),
					frm.doc.tenant_id,
				]),
				false,
			],
			[
				__("Remove"),
				// savetrash confirms and releases the provider reservation.
				() => frm.savetrash(),
				is_detached && !frm.is_new() && frappe.model.can_delete(frm.doctype),
				null,
				true,
			],
		].forEach(([label, action, condition, confirm_message, is_dangerous]) => {
			if (!condition) {
				return;
			}

			frm.add_custom_button(
				label,
				() => (confirm_message ? frappe.confirm(confirm_message, action) : action()),
				is_dangerous ? __("Dangerous Actions") : __("Actions")
			);
		});
	},
});

function showAttachPrefixDialog(frm) {
	const dialog = new frappe.ui.Dialog({
		title: __("Attach {0}/{1} to a Virtual Machine", [frm.doc.address, frm.doc.cidr]),
		fields: [
			{
				fieldname: "virtual_machine",
				fieldtype: "Link",
				label: __("Virtual Machine"),
				options: "Virtual Machine",
				reqd: 1,
				description: __(
					"Its host routes this block to it, answers for the block, and the VM may use its addresses."
				),
			},
		],
		primary_action_label: __("Attach"),
		primary_action(values) {
			frm.call("attach_public_ipv6", { virtual_machine: values.virtual_machine }).then(
				() => {
					dialog.hide();
					frm.reload_doc();
				}
			);
		},
	});
	dialog.show();
}
