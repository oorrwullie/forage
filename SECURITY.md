# Security Policy

## Reporting a vulnerability

Please do not disclose security vulnerabilities through a public GitHub issue.

Use GitHub private vulnerability reporting for this repository when available.

If private vulnerability reporting is unavailable, contact the repository owner
privately before publishing details.

Do not include real credentials, bearer tokens, private prompts, or other
sensitive data in a report.

## Security model

Forage treats route selection and provider execution as a trust boundary.

Important properties include:

- callers explicitly declare data sensitivity;
- unknown sensitivity fails policy evaluation;
- route cost and data-handling policy are explicitly configured;
- candidate routes are advisory until revalidated immediately before dispatch;
- the HTTP facade accepts loopback listen addresses only;
- the HTTP facade requires bearer authentication;
- provider credentials are supplied at runtime rather than persisted in route
  configuration;
- provider-reported effective-model identity is checked when available.

Reports demonstrating a bypass of one of these boundaries are particularly
useful.

## Supported versions

Forage is currently early-stage software. Until stable releases exist, security
fixes are applied to the current development line.
