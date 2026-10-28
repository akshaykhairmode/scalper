#!/usr/bin/env python3
"""Probe Kotak's official Python SDK HSM authentication and index subscription."""

from __future__ import annotations

import argparse
import getpass
import json
import os
import re
import sys
import threading
import time
from typing import Any

DEFAULT_HSM_ENDPOINT = "wss://sfeed.kotaksecurities.com/wsfeed"


def response_status(response: Any, required_data: tuple[str, ...]) -> str:
    if not isinstance(response, dict):
        return "unexpected_response"
    payload = response.get("data")
    if not isinstance(payload, dict):
        payload = response
    for candidate in (response.get("status"), payload.get("status")):
        if isinstance(candidate, str) and candidate.lower() in {
            "success",
            "ok",
            "failed",
            "error",
        }:
            return candidate.lower()
    if any(key in response or key in payload for key in ("Error", "error")):
        return "error"
    if all(payload.get(field) for field in required_data):
        return "success"
    return "unspecified"


def safe_response_details(response: Any, secrets: tuple[str, ...]) -> str:
    if not isinstance(response, dict):
        return "response was not a JSON object"

    safe_values: list[str] = []

    def collect(value: Any, depth: int = 0) -> None:
        if depth > 5:
            return
        if isinstance(value, dict):
            for key, nested in value.items():
                normalized_key = str(key).replace(" ", "_").lower()
                if normalized_key in {
                    "code",
                    "error_code",
                    "errorcode",
                    "status",
                } and isinstance(nested, (str, int)):
                    safe_values.append(f"{key}={nested}")
                elif normalized_key in {
                    "error",
                    "message",
                    "error_message",
                    "error_description",
                    "description",
                    "reason",
                }:
                    if isinstance(nested, str) and nested.strip():
                        safe_values.append(f"{key}={nested.strip()}")
                    else:
                        collect(nested, depth + 1)
                elif isinstance(nested, (dict, list, tuple)):
                    collect(nested, depth + 1)
        elif isinstance(value, (list, tuple)):
            for nested in value:
                collect(nested, depth + 1)

    collect(response)

    if not safe_values:
        keys = sorted(str(key) for key in response.keys())
        error = response.get("error")
        error_type = type(error).__name__ if error is not None else "none"
        return (
            f"no recognized error fields (response keys: "
            f"{', '.join(keys[:12]) or 'none'}; error value type: {error_type})"
        )

    return redact_sensitive("; ".join(safe_values), secrets)


def redact_sensitive(value: str, secrets: tuple[str, ...]) -> str:
    details = value
    for secret in sorted((value for value in secrets if value), key=len, reverse=True):
        details = details.replace(secret, "[redacted]")
    details = re.sub(r"\+?\d[\d\s().-]{7,}\d", "[redacted number]", details)
    details = re.sub(r"\b[A-Za-z0-9_-]{24,}\b", "[redacted value]", details)
    return details[:400]


def install_hsm_metadata_compatibility(sdk: Any) -> threading.Event:
    metadata_received = threading.Event()
    websocket_class = sdk.NeoWebSocket
    original_handler = websocket_class.on_hsm_message

    def handle_hsm_message(websocket: Any, message: Any) -> None:
        if isinstance(message, str):
            try:
                decoded = json.loads(message)
            except json.JSONDecodeError:
                decoded = None
            if (
                isinstance(decoded, dict)
                and str(decoded.get("message_code")) == "1119"
            ):
                metadata_received.set()
                return
        original_handler(websocket, message)

    websocket_class.on_hsm_message = handle_hsm_message
    return metadata_received


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mobile-number", default=os.getenv("KOTAK_MOBILE_NUMBER"))
    parser.add_argument("--ucc", default=os.getenv("KOTAK_UCC"))
    parser.add_argument("--neo-fin-key", default=os.getenv("KOTAK_NEO_FIN_KEY"))
    parser.add_argument("--consumer-key", default=os.getenv("KOTAK_CONSUMER_KEY"))
    parser.add_argument(
        "--endpoint",
        default=os.getenv("KOTAK_HSM_URL", DEFAULT_HSM_ENDPOINT),
        help="HSM WebSocket URL (default: Scalper's configured endpoint)",
    )
    parser.add_argument("--index", default="Nifty 50", help="index name to subscribe to")
    parser.add_argument("--wait-seconds", type=int, default=20)
    args = parser.parse_args()

    access_token = os.getenv("KOTAK_ACCESS_TOKEN", "").strip() or None
    consumer_key = args.consumer_key or getpass.getpass(
        "Kotak consumer key (input hidden): "
    ).strip()
    if not consumer_key:
        parser.error("a Kotak consumer key is required for TOTP login")
    if not args.mobile_number:
        args.mobile_number = input("Registered mobile number (with country code): ").strip()
    if not args.ucc:
        args.ucc = input("Kotak UCC: ").strip()
    if not args.mobile_number or not args.ucc:
        parser.error("mobile number and UCC are required")
    if not 5 <= args.wait_seconds <= 120:
        parser.error("--wait-seconds must be between 5 and 120")

    try:
        import neo_api_client
        from neo_api_client import NeoAPI
    except ImportError as exc:
        print(
            "Kotak SDK is not installed. Install poc/requirements.txt with Python 3.10–3.13.",
            file=sys.stderr,
        )
        print(f"Import error: {type(exc).__name__}", file=sys.stderr)
        return 2

    metadata_received = install_hsm_metadata_compatibility(neo_api_client)
    if not args.endpoint.startswith("wss://"):
        parser.error("--endpoint must use wss://")
    neo_api_client.WEBSOCKET_URL = args.endpoint
    endpoint = args.endpoint
    print(f"SDK version: {getattr(neo_api_client, '__version__', 'unknown')}")
    print(f"HSM endpoint: {endpoint}")

    totp = getpass.getpass("TOTP (input hidden): ").strip()
    mpin = getpass.getpass("MPIN (input hidden): ").strip()
    if not totp or not mpin:
        print("TOTP and MPIN are required.", file=sys.stderr)
        return 2

    feed_event = threading.Event()
    callback_error = threading.Event()
    credential_values = (
        access_token or "",
        consumer_key,
        args.mobile_number,
        args.ucc,
        totp,
        mpin,
    )

    def on_message(message: Any) -> None:
        event_type = message.get("type") if isinstance(message, dict) else None
        print(f"SDK callback: message received (type={event_type or 'unspecified'})")
        if event_type in {"stock_feed", "quotes"}:
            feed_event.set()

    def on_error(error: Any) -> None:
        callback_error.set()
        detail = ""
        if isinstance(error, KeyError) and len(error.args) == 1:
            missing_key = error.args[0]
            if isinstance(missing_key, int):
                detail = f", missing key {missing_key}"
        else:
            message = redact_sensitive(str(error), credential_values)
            if message:
                detail = f": {message}"
        print(f"SDK callback: error ({type(error).__name__}{detail})")

    def on_open(_message: Any = None) -> None:
        print("SDK callback: HSM WebSocket opened; authentication is still pending")

    def on_close(_message: Any = None) -> None:
        print("SDK callback: HSM WebSocket closed")

    client = NeoAPI(
        environment="prod",
        access_token=access_token,
        neo_fin_key=args.neo_fin_key,
        consumer_key=consumer_key,
    )
    client.on_message = on_message
    client.on_error = on_error
    client.on_open = on_open
    client.on_close = on_close

    try:
        print("Starting TOTP login...")
        login_response = client.totp_login(
            mobile_number=args.mobile_number,
            ucc=args.ucc,
            totp=totp,
        )
        login_status = response_status(login_response, ("token", "sid"))
        print(f"TOTP login status: {login_status}")
        if login_status not in {"success", "ok"}:
            print(
                "TOTP login details: "
                + safe_response_details(
                    login_response,
                    (
                        access_token or "",
                        consumer_key,
                        args.mobile_number,
                        args.ucc,
                        totp,
                        mpin,
                    ),
                )
            )
            return 1

        print("Validating MPIN...")
        validate_response = client.totp_validate(mpin=mpin)
        validate_status = response_status(validate_response, ("token", "sid"))
        print(f"MPIN validation status: {validate_status}")
        if validate_status not in {"success", "ok"}:
            print(
                "MPIN validation details: "
                + safe_response_details(
                    validate_response,
                    (
                        access_token or "",
                        consumer_key,
                        args.mobile_number,
                        args.ucc,
                        totp,
                        mpin,
                    ),
                )
            )
            return 1

        print(f"Requesting live index subscription: {args.index} (nse_cm)")
        client.subscribe(
            instrument_tokens=[
                {"instrument_token": args.index, "exchange_segment": "nse_cm"}
            ],
            isIndex=True,
            isDepth=False,
        )

        deadline = time.monotonic() + args.wait_seconds
        hsm_connected = False
        authentication_confirmed = False
        while time.monotonic() < deadline:
            websocket_client = getattr(client, "NeoWebSocket", None)
            hsm_connected = bool(getattr(websocket_client, "is_hsw_open", False))
            authentication_confirmed = authentication_confirmed or hsm_connected
            if feed_event.is_set() or callback_error.is_set():
                break
            time.sleep(0.2)

        if hsm_connected:
            print("SDK HSM authentication: confirmed and connected")
        elif authentication_confirmed:
            print("SDK HSM authentication: confirmed, but the connection closed")
        else:
            print("SDK HSM authentication: not confirmed")
        if metadata_received.is_set():
            print("SDK HSM initialization metadata: received and ignored by POC compatibility shim")
        if feed_event.is_set():
            print("SDK live feed: received subscription data")
            return 0
        if hsm_connected:
            print("SDK live feed: authenticated, but no subscription data received yet")
            return 0
        if authentication_confirmed:
            print("SDK live feed: the HSM connection closed before subscription data arrived")
            return 1
        if callback_error.is_set():
            print("SDK live feed: SDK reported an error during the HSM/feed wait")
        else:
            print(f"SDK live feed: no authentication/feed confirmation within {args.wait_seconds}s")
        return 1
    except Exception as exc:  # SDK may raise transport/API-specific exception classes.
        print(f"SDK probe failed: {type(exc).__name__}")
        return 1
    finally:
        websocket_client = getattr(client, "NeoWebSocket", None)
        hsm_client = getattr(websocket_client, "hsWebsocket", None)
        close = getattr(hsm_client, "close", None)
        if callable(close):
            try:
                close()
            except Exception as exc:  # Keep cleanup diagnostics sanitized.
                print(f"SDK HSM close failed: {type(exc).__name__}", file=sys.stderr)


if __name__ == "__main__":
    raise SystemExit(main())
