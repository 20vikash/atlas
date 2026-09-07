# SSH Task

`SSH Task` records one shell command or script run on a `Metal Server` or a `Virtual Machine`. `SSHRunner` opens the SSH connection and returns the command output and exit code.

## Target

Set `target_type` to `Metal Server` or `Virtual Machine`. Set `target` to the target document name. The task reads the target document's `ssh_host` property for the connection address.

`SSH Task` stores no credentials. It uses the identity of the Atlas host. Make the target reachable before you create the task.

## Create a task

Use `SSHTask.create_for_command` for a command. Use `SSHTask.create_for_script_file` for a script in `atlas/scripts/`.

Pass `script_path` relative to `atlas/scripts/`. Do not use an absolute path or a path that contains `..`.

Do not put a key or password in `script` or `environment`. These fields are stored as plain text. Use `SSHRunner` directly when the command needs secret data. Direct use of `SSHRunner` does not create an `SSH Task` record.

## State and execution

An `SSH Task` has one of these states: `Pending`, `Running`, `Success`, or `Failed`.

Tasks run in the `long` queue by default. Set `run_in_background=False` when the caller must wait for execution to finish.

The task writes output to `output` while the command runs. It writes the exit code and end time after the command finishes.

`timeout_seconds` sets the worker timeout and the SSH command timeout. The value must be between 1 and 3,600 seconds.

Each task in the `Running` state gets an additional 10-second buffer. The scheduler marks the task `Failed` after the buffer expires.
