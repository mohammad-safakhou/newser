# Security Policy

We take the security of Newser seriously. Please follow these guidelines to report vulnerabilities responsibly.

## Supported Versions

| Version | Supported |
|---------|-----------|
| main    | Yes      |
| Releases within the last 6 months | Yes |
| Older releases | Best effort (reporting still appreciated) |

## Reporting a Vulnerability

- Email `security@newser.dev` with the subject "Security disclosure" and provide reproduction details, impact, and any
  suggested mitigations.
- Alternatively, open a private report via GitHub Security Advisories if email is not an option.
- Please do not file public issues for exploitable vulnerabilities.

What to expect:

1. We will acknowledge receipt within **3 business days**.
2. We aim to provide an initial assessment or request for clarification within **5 business days** of acknowledgement.
3. We will coordinate on remediation steps, disclosure timelines, and CVE requests when applicable.
4. After a fix ships, we will credit reporters (unless anonymity is requested) in the release notes and changelog.

## Scope

Security issues that fall under this policy include (but are not limited to):

- Remote code execution, sandbox escapes, or privilege escalation in any Newser service
- Authentication or authorization bypasses, including JWT/session handling flaws
- Leakage of secrets, personally identifiable information, or cross-tenant data
- Cross-site scripting (XSS), CSRF, SSRF, or other injection vulnerabilities in the WebUI/API
- Insecure default configurations that materially reduce system safety

## Out of Scope

The following are generally not considered security vulnerabilities:

- Missing security-related HTTP headers without demonstrated impact
- DoS attacks requiring overwhelming traffic or privileged network access
- Vulnerabilities in third-party dependencies without proof-of-concept showing exploitation within Newser
- Lack of best-practice hardening for self-managed deployments (e.g., reverse proxy configuration)

## Responsible Disclosure

Please give us a reasonable opportunity to resolve issues before any public disclosure. Coordinated disclosure ensures
users have patches available and reduces risk for active deployments. If you do not receive a timely response, you may
escalate by tagging a maintainer in a GitHub discussion referencing your original attempt to contact `security@newser.dev`.
