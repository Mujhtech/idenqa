# Security policy

Idenqa handles security-sensitive identity workflows. Do not include real
identity evidence, credentials, tokens, private keys, or exploitable tenant
data in a report.

## Reporting a vulnerability

Use GitHub's private vulnerability-reporting form for this repository:

<https://github.com/Mujhtech/idenqa/security/advisories/new>

Include the affected revision, deployment assumptions, reproduction steps,
impact, and a minimal synthetic proof. If private reporting is unavailable,
open a content-free issue asking the maintainers for a private channel; do not
publish vulnerability details in that issue.

Maintainers will aim to acknowledge a complete report within three business
days, provide an initial severity assessment within seven business days, and
agree a disclosure timeline with the reporter. Critical issues that affect a
supported release take priority over normal release work. These are response
targets during the pre-release period, not a warranty.

## Supported versions

Idenqa has not issued an external-beta release. Until one exists, only the
current `main` revision is considered for security fixes and it must not be
treated as production-supported. The first external-beta release will replace
this section with an explicit version and patch-support matrix before the tag
is published.

## Coordinated disclosure

Please allow maintainers reasonable time to reproduce, fix, test, and publish
an advisory before public disclosure. Idenqa will credit reporters who request
credit and will coordinate earlier disclosure when active exploitation or
material user risk makes waiting unsafe.
