
**Assignee** (one, nullable) is ownership of a task or decision, separate from leases, with a lease-free start/stop/submit lifecycle and auto-assign on start: `lode task assign`, `start`, `stop`, `submit` emit `task.assigned`, `task.started`, `task.stopped`. A joint task splits rather than sharing an assignee.

**Contributors** are derived: everyone ever assigned a task in the project.

