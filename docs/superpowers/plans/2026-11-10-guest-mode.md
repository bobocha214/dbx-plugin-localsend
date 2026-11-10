# Guest-first LAN transfer update

User approved removing registration/login gates on 2026-11-10. Supersedes invitation requirements in original IM design. Implement in current codex/lan-im branch without deleting old data.

- [x] Backend: POST /guest returns anonymous capability identity; local RPC stable host, browser distinct visitors; automatically join public lan-lobby. Preserve private conversation ACLs and local-only node controls. Disable obsolete login/register/setup public routes; retain old account records as archive.
- [x] UI: no password or invite screens, DBX enters directly and starts LAN service, browser nickname join; retain editable profile and contacts; explain public lobby visibility. Persist anonymous token guarded, never put login token in URL.
- [x] Transfer: remove central2GiB/peer1GiB/128MiB browser arbitrary file caps; retain bounded chunks and preview limits. Use native streaming save or one-use short-lived browser download capability for same-origin central files.
- [x] Verification: test stable host versus separate visitors, public lobby versus private isolation, large file metadata without allocation, one-use downloads, obsolete account routes rejection, JS sandbox click regressions; build0.2.2 Windows candidate.

Implementation decision: opaque guest capabilities protect private histories without requiring users to authenticate. Clearing browser data loses that visitor identity; no nickname-based recovery or merging. Existing password accounts are not silently impersonated or deleted.
