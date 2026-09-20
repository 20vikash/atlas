# Virtual machine migration operation

## Create a migration

Use the **Migrate VM** action in **Dangerous Actions** on a Virtual Machine form. You can select a destination Metal Server or leave it empty. Atlas creates the migration record. Desk users cannot create or edit migration records directly.

## Read migration state

Atlas uses these statuses:

| Status | Meaning |
|---|---|
| `scheduled` | Atlas waits for an eligible destination Metal Server. |
| `preparing` | Atlas reserved the destination and Metal starts the migration. |
| `copying` | Metal copies disk data to the destination. |
| `cutting_over` | Metal stops the source and copies the final disk data. |
| `starting` | Metal starts the VM on the destination. |
| `finalizing` | Atlas changed the VM server and Metal removes the source. |
| `canceling` | Metal removes destination data and restores the source if needed. |
| `completed` | The destination owns the VM. |
| `failed` | The migration could not complete. |
| `aborted` | Atlas canceled the migration. |

The migration record stores its destination selection attempts, timestamps, duration, errors, and one typed row for each disk copy round. Each transfer row stores its start and finish time. It does not store migration JSON.

## Select a destination

Atlas tries a selected destination once. A destination without capacity sets the migration to `failed`.

When no destination is selected, Atlas keeps the migration at `scheduled` and tries again each minute. You can abort it while it waits.

## Read progress

`progress_percent` is an estimate of lifecycle progress. It is not a total copy percentage because a running VM can create more changed data during a migration. Use the transfer fields and transfer rows for copied MiB values.
