# Read-only fileshare drivers

`fileshare.New` validates a server-side `Config` without connecting to the share. `List` returns immediate children of a path; pass an empty path to list the configured root. `Read` requires a relative file path and a positive byte cap (at most 16 MiB). Both operations have a 20-second deadline, or the caller's earlier deadline. Pass the authenticated application's username separately from the share's credentials.

| Protocol | Endpoint | Root | Authentication |
| --- | --- | --- | --- |
| SMB | `smb://files.example:445/team` | `exports/{username}` relative to the named share | NTLM credentials, optionally `DOMAIN\user`; SMB 2/3 with signing; no DFS |
| SFTP | `sftp://files.example:22` | `/exports/{username}` within the SFTP server's namespace | Password and mandatory OpenSSH `SHA256:…` server fingerprint |
| WebDAV | `https://files.example/dav` | `exports/{username}` relative to the endpoint | HTTP Basic; use HTTPS outside isolated test environments |

Use `/` to explicitly select the endpoint's root. The optional `{username}` placeholder must occupy a whole segment. Ensure application usernames are distinct under the server's filename rules, especially on case-insensitive SMB shares. Use a least-privilege, read-only service account and keep credentials in the backend.

Do not enable the service account on an untrusted server or use a link-containing tree as a security boundary. SMB rejects reparse points at every path component. SFTP rejects links reported by the server and checks the canonical path before and after access, but SFTP v3 cannot atomically open a file beneath a verified directory handle: paths must not be concurrently replaceable by untrusted writers, or the server must enforce the same boundary. WebDAV never follows HTTP redirects or DAV redirect references. The WebDAV server must confine backing filesystem lookups; OS-level links the server presents as ordinary resources are invisible to the protocol.
