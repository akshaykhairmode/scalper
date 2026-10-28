# Standalone Gorilla HSM probe

This is a separate Go module. It does not import Scalper packages or read
`config.yaml`. Its only third-party dependency is Gorilla WebSocket; HTTP,
authentication, binary HSM framing, and index feed decoding use the Go standard
library.

The probe performs the TOTP and MPIN REST login flow, opens the HSM WebSocket
with normal TLS certificate verification, sends the binary connection and
subscription frames, and prints decoded index quotes. It does not place orders
or print raw responses or frames. Credentials remain redacted unless
`-show-secrets` is explicitly supplied.

For authentication diagnostics, the output reports whether TOTP/MPIN were
provided and shows the byte lengths of JWT/SID values returned by each login
step and sent in the HSM auth frame. To print the exact TOTP, MPIN, JWT and SID
values for a local diagnostic run, pass `-show-secrets`:

```sh
go run . -show-secrets
```

This prints live authentication credentials to the terminal. Do not share or
save that output; revoke/reset any credential that is accidentally exposed.

From the repository root, set credentials in the environment. The access token,
TOTP, and MPIN can be entered without echoing:

```sh
printf 'Access token: '
read -s KOTAK_ACCESS_TOKEN
printf '\nTOTP: '
read -s KOTAK_TOTP
printf '\nMPIN: '
read -s KOTAK_MPIN
export KOTAK_ACCESS_TOKEN KOTAK_TOTP KOTAK_MPIN
export KOTAK_MOBILE_NUMBER='+91...'
export KOTAK_UCC='...'
export KOTAK_NEO_FIN_KEY='neotradeapi'
```

Run the standalone module:

```sh
cd poc/gorilla-hsm
go run .
```

It defaults to `wss://sfeed.kotaksecurities.com/wsfeed`, subscribes to
`if|nse_cm|Nifty 50`, and listens for one minute. Options:

```sh
go run . -endpoint wss://sfeed.kotaksecurities.com/wsfeed \
  -instrument 'if|nse_cm|Nifty 50' -duration 2m
```

Set `-duration 0` to keep listening until Ctrl-C. The separate module downloads
Gorilla WebSocket on its first build; no Scalper packages are imported.
