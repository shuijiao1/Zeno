# Security Policy / 安全策略

## Report privately / 私密报告

Use GitHub Private Vulnerability Reporting:
<https://github.com/shuijiao1/Zeno/security/advisories/new>

请勿公开未修复漏洞，也不要附上 token、完整安装命令、数据库、备份、私钥或未脱敏日志。Do not publish an unpatched vulnerability or attach tokens, complete install commands, databases, backups, private keys, or unredacted logs.

Maintainers aim to acknowledge a report within **7 days**, provide an initial assessment within **14 days**, and coordinate remediation and disclosure with the reporter. Timelines may vary with severity and reproducibility; do not disclose before a fix and advisory are ready unless required by law.

## Supported versions / 支持版本

Security fixes target the latest stable Controller release and the Agent versions listed as supported in [`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md). Older releases receive best-effort assistance only. If a security fix requires a coordinated Agent upgrade, the advisory and compatibility table will say so.

For trust boundaries, deployment hardening, credential handling, historical disclosure, and recovery, read the full bilingual-linked policy in [`docs/SECURITY.md`](docs/SECURITY.md).
