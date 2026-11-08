# Scalper Mobile Application (iOS & Android)

This document provides complete instructions for developing, configuring, and building the Scalper Mobile Proof-of-Concept (POC) application for Android and iOS using [Capacitor](https://capacitorjs.com/).

---

## 1. Architecture Overview

Scalper Mobile packages the responsive web trading terminal into a lightweight, high-performance native container using Capacitor 6:

```
┌───────────────────────────────────────────────────────────────┐
│                    Scalper Native Container                   │
│                                                               │
│   ┌───────────────────────────────────────────────────────┐   │
│   │                 Native Shell & Controls               │   │
│   │  • Dark Theme (#0b0e14) Status Bar & Navigation Bar   │   │
│   │  • Hardware Accelerated WebView (Android & iOS)       │   │
│   │  • Safe Area Insets (env(safe-area-inset-*))          │   │
│   │  • Network State Monitoring & Permissions             │   │
│   └───────────────────────────────────────────────────────┘   │
│                              │                                │
│   ┌───────────────────────────────────────────────────────┐   │
│   │                 Web Application Layer                 │   │
│   │  • HTML5 / CSS3 / ES6 Modular Terminal UI             │   │
│   │  • KLineCharts Engine & Lightweight Charts            │   │
│   │  • Real-time WebSocket (`/ws`) Stream                 │   │
│   │  • Live Kotak Neo API & Order Feed integration        │   │
│   │  • Local Offline Gateway & Backend Switcher Fallback  │   │
│   └───────────────────────────────────────────────────────┘   │
└──────────────────────────────┬────────────────────────────────┘
                               │
            ┌──────────────────┴──────────────────┐
            ▼                                     ▼
   [Live Hosted Server]                 [Local Development]
https://130.210.18.97.sslip.io        http://10.0.2.2:18080 (Android)
                                      http://localhost:18080 (iOS)
```

### Key Technical Characteristics
- **App ID**: `com.scalper.terminal`
- **App Name**: `Scalper`
- **Dark Theme**: Pure dark theme `#0b0e14` with matching status bar and navigation bar.
- **Hardware Acceleration**: Enabled in `AndroidManifest.xml` and `MainActivity.java` for 60fps charting and real-time tick rendering.
- **Network Permissions**: `INTERNET`, `ACCESS_NETWORK_STATE`, and cleartext development traffic support via `network_security_config.xml`.
- **Notch & Safe Areas**: Responsive safe area handling across iPhone dynamic islands, Android punch-holes, and gesture navigation bars.

---

## 2. Directory Structure

All mobile-specific configurations and native projects reside in the `mobile/` directory:

```
mobile/
├── package.json                   # Capacitor dependencies and CLI scripts
├── capacitor.config.json          # Core Capacitor configuration (App ID, server URL, plugins)
├── scripts/
│   └── sync-assets.js             # Asset synchronization tool from internal/web/static
├── www/                           # Mobile web assets bundle
│   ├── index.html                 # Mobile terminal shell with offline gateway & backend switcher
│   ├── app.css                    # Scalper styling with safe area notch support
│   ├── app.js                     # Core frontend application
│   ├── charts.js                  # Charting integration
│   ├── klinecharts.min.js         # Technical charting engine
│   └── static/                    # Mirrored static assets and icons
├── android/                       # Native Android Studio / Gradle project
│   ├── gradlew                    # Gradle wrapper script (POSIX)
│   ├── gradlew.bat                # Gradle wrapper script (Windows)
│   ├── build.gradle               # Root Gradle build script
│   ├── settings.gradle            # Module declarations
│   └── app/
│       ├── build.gradle           # Android application build configuration
│       └── src/main/
│           ├── AndroidManifest.xml # Permissions (INTERNET, ACCESS_NETWORK_STATE)
│           ├── java/com/scalper/terminal/MainActivity.java
│           └── res/               # Dark theme values, splash, network security configs
└── ios/                           # Native Xcode project
    └── App/
        ├── Podfile                # CocoaPods configuration
        ├── App/
        │   ├── AppDelegate.swift  # iOS lifecycle and dark window setup
        │   └── Info.plist         # iOS permissions and fullscreen settings
        └── App.xcodeproj          # Xcode project file
```

---

## 3. Server URL Configuration & Backend Switcher

Scalper Mobile uses a **zero-hardcoded-URL** pairing architecture. The native mobile binaries (Android APK and iOS App) do not hardcode any server IP or domain. Instead, the mobile app pairs dynamically with your trading terminal by scanning the QR code displayed on the desktop web interface.

### Mode A: Dynamic QR Code Pairing (Default & Recommended)
1. Launch Scalper on your desktop or cloud server.
2. Open the menu and click **Pair Device** to display the one-time device pairing QR code.
3. Open the Scalper mobile app and tap **Scan QR Code**.
4. The mobile camera scans the QR code, securely binds the device with a 1-year cryptographic token, and launches the terminal session immediately.
5. Future app launches remember the paired server URL automatically. To unpair or switch servers, open the mobile drawer and tap **Switch / Unpair Server**.

### Mode B: Local Development / Emulator Endpoints
When testing locally with a running Go server (`./bin/scalper -config config.yaml`):

| Platform | Host URL | Description |
|---|---|---|
| **Android Emulator** | `http://10.0.2.2:18080` | Android emulator loopback alias to host machine |
| **iOS Simulator** | `http://localhost:18080` | macOS loopback to local server |
| **Physical Device (Wi-Fi)** | `http://192.168.x.x:18080` | Host machine's local LAN IP |

### Mode C: Offline Asset Fallback & Pairing Gateway
The bundled `mobile/www/index.html` provides a self-contained Mobile Pairing Gateway:
1. **Live Camera Scanner**: Hardware-accelerated camera QR scanner (`BarcodeDetector` + bundled `jsQR` fallback).
2. **Photo / Image QR Picker**: Upload a screenshot or photo containing a pairing QR code.
3. **Manual URL Entry**: Option to manually enter a server hostname or IP if camera is unavailable.
4. **Live Latency Ping**: Tests `/healthz` on the target backend and displays roundtrip response time.

---

## 4. Prerequisites

To build and run the mobile application locally, ensure you have:

- **Node.js**: v18.0.0 or newer (`node -v`)
- **Java Development Kit**: JDK 17 (`java -version`)
- **Android Development** (for Android APKs):
  - Android Studio Hedgehog (2023.1.1) or newer
  - Android SDK Platform 34 and Build Tools 34.0.0
  - `ANDROID_HOME` environment variable configured
- **iOS Development** (macOS only):
  - Xcode 15 or newer
  - CocoaPods (`sudo gem install cocoapods`)

---

## 5. Local Development Workflow

### 1. Install Dependencies
```bash
cd mobile
npm install
```

### 2. Sync Assets from Scalper Web Frontend
Whenever you modify CSS, JavaScript, or templates in `internal/web/static/`:
```bash
npm run bundle
```
Or use the Makefile helper from the repository root:
```bash
make mobile-sync
```

### 3. Running on Android

#### Open in Android Studio:
```bash
cd mobile
npx cap open android
```
Click **Run** (Green play button) in Android Studio to launch on your connected device or emulator.

#### Direct CLI Run:
```bash
cd mobile
npx cap run android
```

### 4. Running on iOS (macOS only)

#### Open in Xcode:
```bash
cd mobile
npx cap open ios
```
Select a Simulator (e.g. iPhone 15 Pro) and press **Cmd + R**.

#### Direct CLI Run:
```bash
cd mobile
npx cap run ios
```

---

## 6. Building Release APKs & Production Signing

### Generating a Release Signing Keystore
To ensure Android and Google Play Protect do not flag your APK as an untrusted debug binary, generate a formal release keystore:

```bash
keytool -genkeypair -v -keystore mobile/android/app/scalper-release.jks \
  -alias scalper \
  -keypass scalperpassword \
  -storepass scalperpassword \
  -keyalg RSA -keysize 2048 -validity 10000 \
  -dname "CN=Scalper Terminal,OU=Trading,O=Scalper,L=Mumbai,ST=Maharashtra,C=IN"
```

### Command-Line Build (Gradle)
To build a release APK locally using the Gradle wrapper:

```bash
cd mobile/android
chmod +x gradlew
./gradlew assembleRelease
```

The compiled APK will be signed with `scalper-release.jks` (or fallback to your environment keystore) at:
```
mobile/android/app/build/outputs/apk/release/app-release.apk
```

To build a debug APK:
```bash
./gradlew assembleDebug
```
Output:
```
mobile/android/app/build/outputs/apk/debug/app-debug.apk
```

### Avoiding "Remove Security Temporarily" / Play Protect Warning
When sideloading APK files directly (via browser or WhatsApp/Telegram), Android has two layers of security:
1. **"Install Unknown Apps / Allow from this source"**: This is an Android OS security barrier that prompts whenever an app is sideloaded outside an official app store.
2. **"Blocked by Play Protect / Unrecognized app"**: Google Play Protect flags any APK signed with a debug certificate (`CN=Android Debug`). Signing with a proper Release Keystore (v2+v3 signature) avoids the debug flag.
3. **To eliminate all installation security prompts completely**:
   - **Option A (Google Play Internal Testing Track)**: Upload the signed release AAB/APK to Google Play Console under **Internal Testing**. Whitelisted email addresses can install directly via Play Store link with zero warnings or security prompts.
   - **Option B (Google Play Protect Developer Whitelist Appeal)**: Submit your release certificate fingerprint (SHA-256) to [Google Play Protect Appeals](https://support.google.com/googleplay/android-developer/contact/protectappeals) so Google marks your self-distributed app as trusted.

---

## 7. Automated CI/CD Release (`.github/workflows/mobile-release.yml`)

The repository includes a dedicated GitHub Actions workflow to automatically build and release mobile binaries:

### Triggering via Git Tag
Whenever a version tag is pushed (e.g., `v1.0.0`):
```bash
git tag v1.0.0
git push origin v1.0.0
```
The CI workflow automatically:
1. Sets up Node.js 20 and JDK 17.
2. Installs Capacitor dependencies and syncs static assets.
3. Builds the Android APK (`./gradlew assembleRelease`).
4. Computes SHA-256 checksums.
5. Uploads `scalper-android.apk` to workflow artifacts.
6. Attaches `scalper-android.apk` directly to the GitHub Release on the Releases page.
7. Validates the iOS Xcode project on `macos-14` runner.

### Triggering Manually (`workflow_dispatch`)
You can trigger a build manually from the GitHub Actions tab:
- **Build Type**: `release` or `debug`
- **Server URL**: Specify a custom backend URL to embed in the mobile build.

---

## 8. Makefile Shortcuts

From the repository root, you can use these convenient targets:

```bash
make mobile-install   # Installs mobile npm packages
make mobile-bundle    # Synchronizes static web assets to mobile/www
make mobile-sync      # Syncs assets and runs Capacitor sync for Android and iOS
make mobile-android   # Builds the Android APK using gradlew
make mobile-ios       # Validates the Xcode project build
```

---

## 9. Security & Hardware Acceleration Configuration

- **Hardware Acceleration**: Enabled on `<application>` and `<activity>` in `AndroidManifest.xml` and explicitly on the `WebView` in `MainActivity.java` via `setLayerType(View.LAYER_TYPE_HARDWARE, null)`.
- **Cleartext Traffic**: Enabled specifically for `10.0.2.2`, `localhost`, and `*.sslip.io` in `res/xml/network_security_config.xml` to allow secure local development alongside TLS production connections.
- **Safe Area Insets**: Handled using `viewport-fit=cover` and CSS rules:
  ```css
  padding-top: env(safe-area-inset-top, 0);
  padding-bottom: env(safe-area-inset-bottom, 0);
  padding-left: env(safe-area-inset-left, 0);
  padding-right: env(safe-area-inset-right, 0);
  ```
