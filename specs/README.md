# Specs

Internal design records. One spec per tightly-scoped decision: the problem, the argument, what was rejected, and the acceptance criteria. These are written for whoever changes this module next.

They are not the user documentation. A person building an application on the port reads [`README.md`](../README.md) and [`docs/`](../docs); a person writing an adapter reads [`docs/adapters.md`](../docs/adapters.md). Neither should have to open this directory.

| Spec | Status | What it settles |
|---|---|---|
| [001](001-the-managed-agent-port.md) — The managed-agent port | designed | What the port covers and refuses to cover, the three vendor shapes it is measured against, the rules a schema cannot carry, and the schema-first pipeline. |
