package com.scalper.terminal;

import android.graphics.Color;
import android.os.Bundle;
import android.view.View;
import android.view.Window;
import android.view.WindowManager;
import android.webkit.WebSettings;
import android.webkit.WebView;
import androidx.activity.OnBackPressedCallback;
import androidx.core.view.WindowCompat;
import androidx.core.view.WindowInsetsControllerCompat;
import com.getcapacitor.BridgeActivity;

public class MainActivity extends BridgeActivity {

    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        // Apply dark theme background (#0b0e14) to native Window
        Window window = getWindow();
        window.addFlags(WindowManager.LayoutParams.FLAG_DRAWS_SYSTEM_BAR_BACKGROUNDS);
        window.setStatusBarColor(Color.parseColor("#0b0e14"));
        window.setNavigationBarColor(Color.parseColor("#0b0e14"));

        // Ensure status bar icons are light (white) on top of the dark status bar
        WindowInsetsControllerCompat insetsController =
            WindowCompat.getInsetsController(window, window.getDecorView());
        if (insetsController != null) {
            insetsController.setAppearanceLightStatusBars(false);
            insetsController.setAppearanceLightNavigationBars(false);
        }

        // Optimize WebView performance with hardware acceleration and dark surface background
        if (this.bridge != null && this.bridge.getWebView() != null) {
            WebView webView = this.bridge.getWebView();
            webView.setBackgroundColor(Color.parseColor("#0b0e14"));
            webView.setLayerType(View.LAYER_TYPE_HARDWARE, null);

            WebSettings settings = webView.getSettings();
            settings.setDomStorageEnabled(true);
            settings.setDatabaseEnabled(true);
            settings.setAllowFileAccess(true);
            settings.setAllowContentAccess(true);
            settings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);

            try {
                if (androidx.webkit.WebViewFeature.isFeatureSupported(androidx.webkit.WebViewFeature.WEB_AUTHENTICATION)) {
                    androidx.webkit.WebSettingsCompat.setWebAuthenticationSupport(
                        settings,
                        androidx.webkit.WebSettingsCompat.WEB_AUTHENTICATION_SUPPORT_FOR_APP
                    );
                }
            } catch (Throwable ignored) {}
        }

        // Handle Android hardware/gesture back button navigation
        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                if (bridge != null && bridge.getWebView() != null) {
                    WebView webView = bridge.getWebView();
                    // First evaluate in JS if any modal, drawer, scanner or view needs to be dismissed
                    webView.evaluateJavascript(
                        "(function() {" +
                        "  if (typeof window.handleAppBackButton === 'function') {" +
                        "    return window.handleAppBackButton();" +
                        "  }" +
                        "  var evt = new CustomEvent('hardwareBackButton', { cancelable: true });" +
                        "  window.dispatchEvent(evt);" +
                        "  return evt.defaultPrevented;" +
                        "})()",
                        value -> {
                            if (!"true".equals(value)) {
                                if (webView.canGoBack()) {
                                    webView.goBack();
                                } else {
                                    setEnabled(false);
                                    getOnBackPressedDispatcher().onBackPressed();
                                    setEnabled(true);
                                }
                            }
                        }
                    );
                } else {
                    setEnabled(false);
                    getOnBackPressedDispatcher().onBackPressed();
                    setEnabled(true);
                }
            }
        });
    }
}
