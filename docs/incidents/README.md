# Incidents

This folder holds one report for each production or staging incident. A report states the impact, the timeline, the root cause, the fix, and the actions that prevent a repeat.

## Reports

| Date | Environment | Incident | Status |
|---|---|---|---|
| 2026-09-24 | Staging | [Guest disk corruption after a restart](2026-09-24-guest-disk-corruption.md) | Fixed. Prevention actions open. |

## Add a report

1. Copy the structure of the newest report.
2. Name the file `YYYY-MM-DD-short-title.md`, with the date of the incident.
3. Record the full hash of the last affected commit in the header table.
4. Reports are public. Do not include personal data or tenant data, such as people's names, email addresses, tenant hostnames, tenant IDs, and tenant IP addresses.
5. Add a row at the top of the table above.
6. Keep the action table current until every action is done.
