# Correlation IDs and incident granularity

The correlation ID is how the agent recognises an alert it has already seen. It
is a SHA-256 of the alert name plus every label, truncated to 16 hex characters,
computed identically on every replica so no shared state is needed.

## Why every label is included

Including the full label set means two alerts that differ in any label are
treated as different incidents. That is a deliberate choice, and it has a visible
consequence worth understanding before changing it.

Alertmanager on the managed clusters groups critical alerts by
`[alertname, namespace]`. Sibling alerts that differ only in a label
Alertmanager is *not* grouping on — two degraded cluster operators, two etcd
members — therefore arrive together in a single webhook payload. The handler
processes each element of `payload.Alerts`, so each sibling gets its own
incident, created seconds apart from one request.

Those siblings have **different** correlation IDs, so the dedup check on the
firing path does not merge them. This is correct under the current definition:
by that definition they are different alerts. It also means dedup is not the fix
for near-simultaneous duplicate *pairs* — see "Two mechanisms" below.

## Delimiters are load-bearing

Keys and values are joined with `\n` and `=`, neither of which can appear in a
Prometheus label name. Without them the canonical string is ambiguous:
`{"a":"bc","d":"e"}` and `{"a":"b","cd":"e"}` both flatten to `abcde` and hash
to the same ID.

Before dedup existed this was harmless — a collision just meant two unrelated
incidents shared a `correlation_id` field. With dedup on the firing path a
collision means **one alert silently suppresses an unrelated one**. Do not
remove the delimiters. `TestCorrelationIDNoDelimiterCollision` guards this.

## Two mechanisms produce duplicates

Both are real, and they need different fixes:

| Mechanism | Cause | Fix |
|---|---|---|
| Repeat-interval stacking | Alertmanager re-notifies still-firing alerts every `repeat_interval` (6h on the critical route). Identical correlation ID each time. | The dedup check on the firing path. Fixed. |
| Batch fan-out | Sibling alerts differing in an ungrouped label arrive in one payload. Different correlation IDs. | Not a dedup fix — see below. |

To tell which one you are looking at, read the agent log on a forwarding
cluster:

```bash
oc logs -n alert2snow -l app=alert2snow-agent --tail=500 \
  | grep -E 'received alertmanager webhook|processing firing alert'
```

`alert_count: 2` in one webhook line with two different correlation IDs is
fan-out. Two separate webhook lines with the same correlation ID is repeat
delivery, which dedup now collapses.

## The granularity decision (not yet made)

If fan-out is the dominant mechanism, there are three options, in increasing
order of blast radius:

1. **Aggregate the volatile label away in the alerting rule.** Narrowest scope,
   no agent change.
2. **Add the distinguishing label to Alertmanager's `group_by`.** Changes
   notification grouping, not incident identity.
3. **Narrow what `GenerateCorrelationID` hashes** to a stable subset
   (`alertname`, `cluster`, `namespace`).

Option 3 is the one to be careful with. It is a service-management decision
about what on-call should see — one incident per cluster per alert name, or one
per distinct label set — not a bug fix, and it carries a migration cost:

- Distinct genuine problems would merge into a single incident.
- **It changes the correlation ID of every currently-open incident**, so
  auto-close breaks for anything mid-flight. The agent would no longer find the
  incident it opened under the old ID, leaving it open forever with no alert
  behind it.

If option 3 is chosen, land it on a boundary where nothing is firing, and plan
to reconcile the orphaned open incidents.

`TestCorrelationIDDependsOnFullLabelSet` pins the current behaviour. It failing
means granularity changed — if that was deliberate, update the test and this
document together.

## Residual duplicate window

The dedup check is read-then-write, so with two replicas two near-simultaneous
webhooks for the same correlation ID can both see "no open incident" before
either create returns. This narrows duplicates from *always* to *only under
near-simultaneous delivery*; it does not eliminate them.

Closing it properly needs a uniqueness constraint in ServiceNow on
`correlation_id` for open incidents, so the second create fails instead of
duplicating. **Do not close it by setting `replicas: 1`** — that puts a single
point of failure on the only automated path from critical alerts to ServiceNow,
and the PodDisruptionBudget (`minAvailable: 1`) would make any node drain block
or drop alerts.
