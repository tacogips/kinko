# Design References

This directory contains reference materials for system design and implementation.

## External References

| Name | URL | Description |
|------|-----|-------------|
| Go Documentation | https://go.dev/doc/ | Official Go documentation |
| Effective Go | https://go.dev/doc/effective_go | Go best practices and idioms |
| Standard Go Project Layout | https://github.com/golang-standards/project-layout | Standard project structure conventions |
| Argon2 (RFC 9106) | https://www.rfc-editor.org/rfc/rfc9106 | Password hashing and key derivation guidance |
| libsodium Secretbox / AEAD Guidance | https://doc.libsodium.org/secret-key_cryptography/aead | Practical AEAD cryptography reference |
| HashiCorp Vault Concepts | https://developer.hashicorp.com/vault/docs/concepts | Secret lifecycle and threat-model design ideas |
| Bitwarden Security Whitepaper | https://bitwarden.com/help/bitwarden-security-white-paper/ | Client-side encryption and key handling reference |
| 1Password Security Design | https://support.1password.com/1password-security/ | Security architecture reference for local+sync vault models |

## Reference Documents

Reference documents should be organized by topic:

```text
references/
├── README.md              # This index file
├── golang/                # Go patterns and practices
└── <topic>/               # Other topic-specific references
```

## Adding References

When adding new reference materials:

1. Create a topic directory if it does not exist.
2. Add reference documents with clear naming.
3. Update this README.md with the reference entry.
