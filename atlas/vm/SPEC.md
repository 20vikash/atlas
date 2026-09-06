# Virtual machine module specification

[App specification](../SPEC.md)

[Human entry point](README.md)

## Purpose

The virtual machine module owns Atlas request records, placement, Metal transport, images, and user workflows.

## Ownership

Keep DocType methods as permission and lifecycle boundaries. Keep cross-document and Metal operations in `core/`.

Metal owns mutable virtual machine desired state and observed state. Atlas keeps only virtual views of this state.

`MetalClient` owns `/v1` HTTP transport. `metal_models.py` validates nested desired and observed VM responses.

Virtual Machine properties read the typed response. Atlas does not copy this mutable state into stored DocType fields.

`models.py` owns validated request values. `PlacementService` owns fresh capacity selection and Server row locks.

`VirtualMachineService` owns cross-document operations, Metal mutations, uncertain draft recovery, and error translation. The Virtual Machine controller keeps permissions and local validation.

`image_transfer.py` owns the Machine image transfer state. `multipart_upload.py` owns S3 multipart rules and validation. `image_builder.py` owns Ubuntu image creation and publication.

## Invariants

- The Virtual Machine name is the Metal virtual machine ID.
- Atlas commits a draft before a create request.
- Placement subtracts every VM request that is newer than the selected capacity sample.
- Placement counts every uncertain draft as a capacity reservation.
- Placement locks and checks the candidate Server again before it inserts the draft.
- Atlas keeps an uncertain draft until Metal confirms presence or absence.
- A Metal read fault is visible. Only an absent virtual machine has no Metal information.
- Placement identifies a missing current capacity sample.
- Public IPv4 changes preserve the current intent version check.
- Image transfer failures keep the identifiers that a retry needs.
- Atlas records an upload-start error on the image.
- The Virtual Machine schema remains unchanged during this refactor.

## Tests

```sh
ruff check atlas
pilot --site TEST_SITE run-tests --module atlas.vm.core.test_placement
pilot --site TEST_SITE run-tests --module atlas.vm.core.test_virtual_machine_service
pilot --site TEST_SITE run-tests --module atlas.vm.core.test_image_builder
pilot --site TEST_SITE run-tests --module atlas.vm.core.test_console_token
pilot --site TEST_SITE run-tests --module atlas.vm.doctype.virtual_machine.test_virtual_machine
pilot --site TEST_SITE run-tests --module atlas.vm.doctype.virtual_machine_image.test_virtual_machine_image
```
