# Daily Drive Health

## Summary

Operators can see the latest SMART or NVMe health reading for selected Linux host drives in `go-motd` without granting disk access to login users or the container status agent.

## Behavior

1. Drive health is off by default. An administrator opts in and assigns each monitored physical drive a stable identifier and a short display label. The feature does not discover or monitor other drives automatically. An empty list or duplicate identifiers or labels is a configuration error.
2. The standalone collector runs daily, independently of logins. It catches up after a missed run when the host comes back online; it does not replay each missed day. An administrator can request an immediate reading. Multiple requests do not run collection concurrently.
3. Collection reads existing telemetry only. It does not start self-tests, enable SMART, modify drive settings, or deliberately wake a sleeping ATA drive. Unsupported devices and sleeping drives remain visible as unknown. Enable only one polling owner per drive; if another service already polls it, retain that service and leave this collector disabled for that drive unless migrating ownership.
4. Each configured drive has one result: **healthy** means the required health indicators were read and passed; **warning** means the drive reports a current health advisory without an explicit failure; **failed** means the drive reports a failed overall health assessment or an explicit critical condition; **unknown** means there is not enough evidence to classify it. Healthy does not guarantee that a drive will not fail.
5. A missing drive, denied access, unsupported bridge, sleeping drive, timeout, or unreadable response yields unknown for that drive without hiding results for other drives. Replacing a drive requires updating its configured identity; a different drive must not inherit its predecessor's reading.
6. When enabled, `go-motd` shows one concise `Drives` row: `healthy` when all readings passed, or `degraded` with only the affected labels and their states. Warnings, failures, and unknowns are explicit in text and do not depend on color. JSON includes observation time and per-drive label, state, and reason. No serial numbers, raw device paths, or raw telemetry are shown.
7. A snapshot older than 36 hours is stale. A stale, missing, unreadable, invalid, or unsupported snapshot never renders as healthy or disappears silently when the feature is enabled. The terminal and JSON distinguish stale from unavailable, and debug output explains why. A subsequent valid fresh snapshot restores normal reporting.
8. Logging in only reads the latest snapshot; it never starts disk I/O for a health query, waits for collection, or requires elevated privileges. Container status and unrelated MOTD sections are unchanged. The feature does not send notifications.

Visual reference: use the existing compact `go-motd` system output; no new layout.
