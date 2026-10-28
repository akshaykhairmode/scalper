# Kotak Python SDK HSM probe

This is an isolated proof of concept that imports Kotak's official Python SDK as
a pinned external dependency. The SDK source is not copied into this repository.
It performs TOTP login, MPIN validation, and an index subscription through the
SDK; it never places or modifies an order.

The probe reports status, event types, connection state, and a bounded,
sanitized summary of login errors. TOTP and MPIN are prompted with hidden input.
The consumer key required for the SDK's TOTP login can be supplied through
`KOTAK_CONSUMER_KEY` or entered when prompted.
Mobile number, UCC, Neo fin key, and (if needed by your setup) access token can
be provided through `KOTAK_MOBILE_NUMBER`, `KOTAK_UCC`, `KOTAK_NEO_FIN_KEY`,
and `KOTAK_ACCESS_TOKEN`.

Use Python 3.10–3.13, as required by the SDK:

```sh
python3.11 -m venv .venv
. .venv/bin/activate
python -m pip install -r poc/requirements.txt
python poc/kotak_hsm_sdk.py
```

The probe overrides the SDK's old built-in HSM URL and defaults to Scalper's
configured endpoint, `wss://sfeed.kotaksecurities.com/wsfeed`. You can supply a
different HSM URL with `--endpoint` or `KOTAK_HSM_URL`:

```sh
python poc/kotak_hsm_sdk.py --endpoint wss://your-hsm-endpoint
```

The selected endpoint is applied to the SDK's exported `WEBSOCKET_URL` before
the SDK client is created. The SDK's HSM text-frame parser expects array-shaped
JSON; the POC wraps that handler to ignore the known object-shaped
`message_code: 1119` exchange initialization frame observed from Scalper's
endpoint, then passes other messages through to the unmodified SDK handler.
The SDK package itself is not changed. The probe exits non-zero if
authentication is not confirmed or the SDK reports an error.
