//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func TestBrowserSmokeAuthLoginSwitchesFromConditionalToExplicit(t *testing.T) {
	app := newAuthEnabledTestApp(t)
	staticRoot := filepath.Join(pathParentDir, pathParentDir, "static")
	app.SetStaticFS(os.DirFS(staticRoot))
	seedAuthCredential(t, app)

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	passkeyStub := `(() => {
		window.__conditionalAttempts = 0;
		window.__requiredAttempts = 0;
		window.__requiredHadActivation = false;
		window.__conditionalAborts = 0;
		Object.defineProperty(window, "PublicKeyCredential", {
			configurable: true,
			value: Object.assign(function PublicKeyCredential() {}, {
				isConditionalMediationAvailable: () => Promise.resolve(true),
			}),
		});
		Object.defineProperty(navigator, "credentials", {
			configurable: true,
			value: {
				get: (options) => {
					if (options.mediation === "conditional") {
						window.__conditionalAttempts += 1;
						return new Promise((resolve, reject) => {
							options.signal.addEventListener("abort", () => {
								window.__conditionalAborts += 1;
								reject(new DOMException("aborted", "AbortError"));
							}, { once: true });
						});
					}
					window.__requiredAttempts += 1;
					window.__requiredHadActivation = navigator.userActivation.isActive;
					return new Promise((resolve, reject) => {
						window.__rejectRequired = () => reject(
							new DOMException("prompt dismissed", "NotAllowedError")
						);
					});
				},
			},
		});
	})();`

	runActions(
		t,
		ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(passkeyStub).Do(ctx)

			return err
		}),
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL+"/auth/login"),
	)
	waitForJS(t, ctx, `(() => window.__conditionalAttempts === 1)()`, "conditional passkey request")
	waitForJS(
		t,
		ctx,
		elementVisibleExpression(`[data-auth-passkey-selector]`),
		"conditional passkey selector",
	)
	waitForJS(
		t,
		ctx,
		`(() => {
			const card = document.querySelector(".auth-card");
			const button = document.querySelector("[data-passkey-login='true']");
			const recovery = document.querySelector(".auth-recovery-link");
			const message = document.querySelector("[data-auth-message]");
			if (!card || !button || !recovery || !message) return false;
			const cardRect = card.getBoundingClientRect();
			const buttonRect = button.getBoundingClientRect();
			return cardRect.left >= 0 && cardRect.right <= innerWidth &&
				buttonRect.height >= 44 && buttonRect.width >= cardRect.width * 0.75 &&
				recovery.getBoundingClientRect().height > 0 && message.getBoundingClientRect().height > 0 &&
				message.textContent.trim() === "" && !message.classList.contains("error");
		})()`,
		"responsive login card ready state",
	)
	runActions(
		t,
		ctx,
		chromedp.Click(`[data-passkey-login="true"]`, chromedp.ByQuery),
	)
	waitForJS(t, ctx, `(() => window.__conditionalAborts === 1)()`, "conditional request abort")
	waitForJS(
		t,
		ctx,
		`(() => window.__requiredAttempts === 1 && window.__requiredHadActivation)()`,
		"explicit passkey request with transient activation",
	)
	waitForJS(
		t,
		ctx,
		`(() => {
			const button = document.querySelector("[data-passkey-login='true']");
			const message = document.querySelector("[data-auth-message]");
			return button.disabled && button.getAttribute("aria-busy") === "true" &&
				message.textContent.trim() === "" && !message.classList.contains("error");
		})()`,
		"explicit passkey pending state",
	)
	runActions(t, ctx, chromedp.Evaluate(`window.__rejectRequired()`, nil))
	waitForJS(
		t,
		ctx,
		`(() => {
			const button = document.querySelector("[data-passkey-login='true']");
			const message = document.querySelector("[data-auth-message]");
			return !button.disabled && !button.hasAttribute("aria-busy") &&
				message.classList.contains("error") &&
				message.textContent.includes("canceled") &&
				!message.textContent.includes("private mode");
		})()`,
		"explicit passkey canceled state",
	)
}

func TestBrowserSmokeAuthLoginUnsupportedFallback(t *testing.T) {
	app := newAuthEnabledTestApp(t)
	staticRoot := filepath.Join(pathParentDir, pathParentDir, "static")
	app.SetStaticFS(os.DirFS(staticRoot))
	seedAuthCredential(t, app)

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	passkeyStub := `(() => {
		window.__passkeyAttempts = 0;
		Object.defineProperty(window, "PublicKeyCredential", {
			configurable: true,
			value: function PublicKeyCredential() {},
		});
		Object.defineProperty(navigator, "credentials", {
			configurable: true,
			value: {
				get: () => {
					window.__passkeyAttempts += 1;
					return new Promise(() => {});
				},
			},
		});
	})();`

	runActions(
		t,
		ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(passkeyStub).Do(ctx)

			return err
		}),
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL+"/auth/login"),
	)
	waitForJS(
		t,
		ctx,
		`(() => window.__passkeyAttempts === 0 &&
			document.querySelector("[data-auth-passkey-selector]").hidden &&
			document.querySelector("[data-passkey-login='true']").getBoundingClientRect().height >= 44 &&
			document.querySelector(".auth-recovery-link").getBoundingClientRect().height > 0)()`,
		"unsupported browser fallback ready state",
	)
	runActions(t, ctx, chromedp.Click(`[data-passkey-login="true"]`, chromedp.ByQuery))
	waitForJS(t, ctx, `(() => window.__passkeyAttempts === 1)()`, "unsupported browser explicit request")
}
