(() => {
  "use strict";

  const base64ToBytes = (value) => {
    const padded = value.replace(/-/g, "+").replace(/_/g, "/");
    const padLength = (4 - (padded.length % 4)) % 4;
    const normalized = padded + "=".repeat(padLength);
    const binary = atob(normalized);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i += 1) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  };

  const bytesToBase64 = (buffer) => {
    const bytes = new Uint8Array(buffer);
    let binary = "";
    for (let i = 0; i < bytes.length; i += 1) {
      binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  };

  const decodePublicKeyCreation = (options) => {
    const decoded = { ...options };
    decoded.challenge = base64ToBytes(options.challenge);
    decoded.user = { ...options.user, id: base64ToBytes(options.user.id) };
    if (Array.isArray(options.excludeCredentials)) {
      decoded.excludeCredentials = options.excludeCredentials.map((credential) => ({
        ...credential,
        id: base64ToBytes(credential.id),
      }));
    }
    return decoded;
  };

  const decodePublicKeyRequest = (options) => {
    const decoded = { ...options };
    decoded.challenge = base64ToBytes(options.challenge);
    if (Array.isArray(options.allowCredentials)) {
      decoded.allowCredentials = options.allowCredentials.map((credential) => ({
        ...credential,
        id: base64ToBytes(credential.id),
      }));
    }
    return decoded;
  };

  const credentialToJSON = (credential) => {
    const response = {
      clientDataJSON: bytesToBase64(credential.response.clientDataJSON),
    };

    if (credential.response.attestationObject) {
      response.attestationObject = bytesToBase64(credential.response.attestationObject);
    }

    if (credential.response.authenticatorData) {
      response.authenticatorData = bytesToBase64(credential.response.authenticatorData);
    }

    if (credential.response.signature) {
      response.signature = bytesToBase64(credential.response.signature);
    }

    if (credential.response.userHandle) {
      response.userHandle = bytesToBase64(credential.response.userHandle);
    }

    const payload = {
      id: credential.id,
      rawId: bytesToBase64(credential.rawId),
      type: credential.type,
      response,
    };

    if (credential.authenticatorAttachment) {
      payload.authenticatorAttachment = credential.authenticatorAttachment;
    }

    if (typeof credential.getClientExtensionResults === "function") {
      payload.clientExtensionResults = credential.getClientExtensionResults();
    }

    if (typeof credential.getTransports === "function") {
      payload.response.transports = credential.getTransports();
    }

    return payload;
  };

  const getCSRFToken = () => {
    const meta = document.querySelector('meta[name="csrf-token"]');
    if (!meta) {
      return "";
    }
    return (meta.getAttribute("content") || "").trim();
  };

  const postJSON = async (url, payload, { signal } = {}) => {
    const headers = {
      "Content-Type": "application/json",
    };

    const csrfToken = getCSRFToken();
    if (csrfToken) {
      headers["X-CSRF-Token"] = csrfToken;
    }

    const response = await fetch(url, {
      method: "POST",
      headers,
      credentials: "same-origin",
      body: JSON.stringify(payload),
      signal,
    });

    if (!response.ok) {
      const detail = (await response.text()).trim();
      const suffix = detail ? ` ${detail}` : "";
      throw new Error(`request failed: ${response.status}${suffix}`);
    }

    return response.json();
  };

  const showMessage = (message, isError) => {
    const node =
      document.querySelector("[data-auth-message]") ||
      document.querySelector(".message");
    if (!node) {
      return;
    }

    const text = (message || "").trim();

    node.textContent = text;
    node.classList.toggle("error", text !== "" && Boolean(isError));
    node.classList.toggle("success", text !== "" && !isError);
  };

  const authErrorMessage = (error, operation) => {
    const fallback = operation === "login" ? "Passkey login failed." : "Passkey registration failed.";
    if (!error || typeof error !== "object") {
      return fallback;
    }

    const message = typeof error.message === "string" ? error.message : "";

    if (message.startsWith("request failed: 401")) {
      return "Authentication failed.";
    }

    if (message.startsWith("request failed: 429")) {
      return "Too many attempts. Wait a few minutes and try again.";
    }

    if (error.name === "NotAllowedError") {
      return "Passkey request was canceled or blocked. In private mode, approve the browser passkey prompt.";
    }

    if (error.name === "SecurityError") {
      return "Passkey request blocked by browser security checks. Confirm the exact HTTPS domain.";
    }

    if (error.name === "NotSupportedError") {
      return "Passkeys are not supported in this browser mode.";
    }

    return fallback;
  };

  const setLoginFallbackVisible = (visible) => {
    const fallback = document.querySelector("[data-auth-login-fallback]");
    if (fallback) {
      fallback.hidden = !visible;
    }

    const pending = document.querySelector("[data-auth-login-pending]");
    if (pending) {
      pending.hidden = visible;
    }
  };

  const setLoginAutoFillVisible = (visible) => {
    const autoFill = document.querySelector("[data-auth-login-autofill]");
    if (autoFill) {
      autoFill.hidden = !visible;
    }
  };

  const isPasskeyLoginSupported = () => Boolean(window.PublicKeyCredential && navigator.credentials);

  const isAbortError = (error) => Boolean(error && typeof error === "object" && error.name === "AbortError");

  const newAbortError = () => {
    const error = new Error("passkey request aborted");
    error.name = "AbortError";
    return error;
  };

  const throwIfLoginCanceled = (signal, shouldContinue) => {
    if (signal && signal.aborted) {
      throw newAbortError();
    }

    if (typeof shouldContinue === "function" && !shouldContinue()) {
      throw newAbortError();
    }
  };

  const startLogin = async ({ mediation = "", onCredential, onOptions, shouldContinue, signal } = {}) => {
    const optionsData = await postJSON("/auth/webauthn/login/options", {}, { signal });
    if (typeof onOptions === "function") {
      onOptions(optionsData);
    }
    throwIfLoginCanceled(signal, shouldContinue);

    const assertion = optionsData.options || {};
    const publicKey = decodePublicKeyRequest(assertion.publicKey || {});

    const requestOptions = { publicKey };
    if (mediation) {
      requestOptions.mediation = mediation;
    } else if (assertion.mediation && assertion.mediation !== "conditional") {
      requestOptions.mediation = assertion.mediation;
    }
    if (signal) {
      requestOptions.signal = signal;
    }

    const credential = await navigator.credentials.get(requestOptions);
    throwIfLoginCanceled(signal, shouldContinue);

    if (!credential) {
      throw new Error("no credential selected");
    }

    if (typeof onCredential === "function") {
      onCredential();
    }

    const verify = await postJSON("/auth/webauthn/login/verify", {
      challenge_id: optionsData.challenge_id,
      credential: credentialToJSON(credential),
    });

    if (verify.redirect) {
      window.location.assign(verify.redirect);
      return;
    }

    window.location.assign("/");
  };

  const startRegistration = async () => {
    const optionsData = await postJSON("/auth/webauthn/register/options", {});
    const creation = optionsData.options || {};
    const publicKey = decodePublicKeyCreation(creation.publicKey || {});

    const createOptions = { publicKey };
    if (creation.mediation && creation.mediation !== "conditional") {
      createOptions.mediation = creation.mediation;
    }

    const credential = await navigator.credentials.create(createOptions);

    if (!credential) {
      throw new Error("registration cancelled");
    }

    await postJSON("/auth/webauthn/register/verify", {
      challenge_id: optionsData.challenge_id,
      credential: credentialToJSON(credential),
    });

    window.location.reload();
  };

  const bindPasskeyLogin = () => {
    const button = document.querySelector("[data-passkey-login='true']");
    if (!button || button.dataset.bound === "true") {
      return;
    }

    button.dataset.bound = "true";
    let conditionalController = null;
    let conditionalCredentialSelected = false;
    let conditionalExpiryTimer = null;
    let conditionalLoginPromise = null;
    let conditionalStarted = false;
    let loginPageHidden = false;
    let manualLoginInProgress = false;
    let manualLoginRequested = false;

    const clearConditionalExpiryTimer = () => {
      if (conditionalExpiryTimer === null) {
        return;
      }

      window.clearTimeout(conditionalExpiryTimer);
      conditionalExpiryTimer = null;
    };

    const abortConditionalLogin = () => {
      const pendingLogin = conditionalLoginPromise;
      if (conditionalController) {
        conditionalController.abort();
      }

      clearConditionalExpiryTimer();
      return pendingLogin;
    };

    const runModalLogin = async (autoStart, mediation) => {
      if (button.dataset.running === "true") {
        return;
      }

      if (!isPasskeyLoginSupported()) {
        setLoginFallbackVisible(true);
        showMessage("Passkeys are not supported in this browser.", true);
        return;
      }

      if (autoStart) {
        setLoginFallbackVisible(false);
        showMessage("Approve the passkey prompt to continue.", false);
      } else {
        setLoginFallbackVisible(true);
        showMessage("", false);
      }

      button.dataset.running = "true";
      button.disabled = true;

      try {
        await startLogin({ mediation });
      } catch (error) {
        console.warn("passkey login failed", error);
        setLoginFallbackVisible(true);
        showMessage(authErrorMessage(error, "login"), true);
      } finally {
        button.dataset.running = "false";
        button.disabled = false;
      }
    };

    const startConditionalLogin = () => {
      if (conditionalStarted || loginPageHidden || manualLoginRequested) {
        return Promise.resolve();
      }

      const autoFillInput = document.querySelector("[data-passkey-autofill='true']");
      if (!autoFillInput || !isPasskeyLoginSupported() || typeof window.AbortController !== "function") {
        setLoginAutoFillVisible(false);
        setLoginFallbackVisible(true);
        return Promise.resolve();
      }

      conditionalStarted = true;
      conditionalCredentialSelected = false;
      const controller = new window.AbortController();
      let conditionalExpired = false;
      conditionalController = controller;
      setLoginAutoFillVisible(true);
      setLoginFallbackVisible(true);
      showMessage("", false);

      let promise;
      promise = (async () => {
        try {
          await startLogin({
            mediation: "conditional",
            onCredential: () => {
              conditionalCredentialSelected = true;
            },
            onOptions: (optionsData) => {
              const expiresAt = Date.parse(optionsData.expires_at || "");
              if (!Number.isFinite(expiresAt)) {
                return;
              }

              const delay = Math.max(0, expiresAt - Date.now() - 5000);
              if (delay === 0) {
                conditionalExpired = true;
                controller.abort();
                return;
              }

              conditionalExpiryTimer = window.setTimeout(() => {
                conditionalExpiryTimer = null;
                conditionalExpired = true;
                controller.abort();
              }, delay);
            },
            shouldContinue: () => !manualLoginRequested && !loginPageHidden,
            signal: controller.signal,
          });
        } catch (error) {
          if (controller.signal.aborted || isAbortError(error)) {
            if (conditionalExpired && !manualLoginRequested) {
              setLoginAutoFillVisible(false);
              setLoginFallbackVisible(true);
              showMessage("", false);
            }
            return;
          }

          if (error && typeof error === "object" && error.name === "NotAllowedError") {
            setLoginAutoFillVisible(true);
            setLoginFallbackVisible(true);
            showMessage("", false);
            return;
          }

          console.warn("conditional passkey login failed", error);
          setLoginAutoFillVisible(false);
          setLoginFallbackVisible(true);
          showMessage(authErrorMessage(error, "login"), true);
        } finally {
          if (conditionalController === controller) {
            conditionalController = null;
            conditionalCredentialSelected = false;
            clearConditionalExpiryTimer();
          }
          if (conditionalLoginPromise === promise) {
            conditionalLoginPromise = null;
          }
        }
      })();
      conditionalLoginPromise = promise;
      return promise;
    };

    const startMobileConditionalLogin = async () => {
      setLoginFallbackVisible(true);

      if (
        !isPasskeyLoginSupported() ||
        typeof window.PublicKeyCredential.isConditionalMediationAvailable !== "function"
      ) {
        setLoginAutoFillVisible(false);
        return;
      }

      let conditionalAvailable = false;
      try {
        conditionalAvailable = await window.PublicKeyCredential.isConditionalMediationAvailable();
      } catch (error) {
        console.warn("conditional passkey login is unavailable", error);
      }

      if (loginPageHidden || manualLoginRequested || !conditionalAvailable) {
        setLoginAutoFillVisible(false);
        return;
      }

      await startConditionalLogin();
    };

    button.addEventListener("click", async () => {
      if (conditionalCredentialSelected || manualLoginInProgress) {
        return;
      }

      manualLoginRequested = true;
      manualLoginInProgress = true;
      const pendingConditionalLogin = abortConditionalLogin();
      setLoginAutoFillVisible(false);
      button.disabled = true;

      try {
        if (pendingConditionalLogin) {
          await pendingConditionalLogin;
        }
        await runModalLogin(false, "required");
      } finally {
        manualLoginInProgress = false;
        if (button.dataset.running !== "true") {
          button.disabled = false;
        }
      }
    });

    if (button.dataset.passkeyAutostartBound === "true" || button.dataset.passkeyAutostart !== "true") {
      return;
    }

    button.dataset.passkeyAutostartBound = "true";
    const isMobileLogin =
      typeof window.matchMedia === "function" && window.matchMedia("(max-width: 960px)").matches;
    if (isMobileLogin) {
      void startMobileConditionalLogin();
      window.addEventListener("pagehide", () => {
        loginPageHidden = true;
        void abortConditionalLogin();
      });
      window.addEventListener("pageshow", (event) => {
        if (!event.persisted || manualLoginRequested) {
          return;
        }

        void (async () => {
          const pendingConditionalLogin = conditionalLoginPromise;
          if (pendingConditionalLogin) {
            await pendingConditionalLogin;
          }
          if (manualLoginRequested) {
            return;
          }

          loginPageHidden = false;
          conditionalStarted = false;
          setLoginAutoFillVisible(false);
          await startMobileConditionalLogin();
        })();
      });
      return;
    }

    window.setTimeout(() => {
      if (!manualLoginRequested) {
        void runModalLogin(true, "");
      }
    }, 0);
  };

  const bindPasskeyRegister = () => {
    const buttons = document.querySelectorAll("[data-passkey-register='true']");
    if (!buttons.length) {
      return;
    }

    buttons.forEach((button) => {
      if (button.dataset.bound === "true") {
        return;
      }

      button.dataset.bound = "true";
      button.addEventListener("click", async () => {
        if (button.dataset.running === "true") {
          return;
        }

        if (!window.PublicKeyCredential || !navigator.credentials) {
          showMessage("Passkeys are not supported in this browser.", true);
          return;
        }

        button.dataset.running = "true";
        button.disabled = true;
        if (button.dataset.autostarted === "true") {
          showMessage("Approve the passkey prompt to finish setup.", false);
        } else {
          showMessage("", false);
        }

        try {
          await startRegistration();
        } catch (error) {
          console.warn("passkey registration failed", error);
          showMessage(authErrorMessage(error, "register"), true);
        } finally {
          button.dataset.running = "false";
          button.disabled = false;
        }
      });
    });
  };

  const autoStartPasskeyRegister = () => {
    const button = document.querySelector("[data-passkey-register='true'][data-passkey-autostart='true']");
    if (!button || button.dataset.autostarted === "true") {
      return;
    }

    button.dataset.autostarted = "true";
    button.click();
  };

  document.addEventListener("DOMContentLoaded", () => {
    bindPasskeyLogin();
    bindPasskeyRegister();
    autoStartPasskeyRegister();
  });
})();
