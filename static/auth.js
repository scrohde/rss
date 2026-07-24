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

  const postJSON = async (url, payload, signal) => {
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
      return "Passkey sign-in was canceled. Try again when you are ready.";
    }

    if (error.name === "SecurityError") {
      return "Passkey request blocked by browser security checks. Confirm the exact HTTPS domain.";
    }

    if (error.name === "NotSupportedError") {
      return "Passkeys are not supported in this browser mode.";
    }

    return fallback;
  };

  const loginNext = () => {
    const shell = document.querySelector("[data-auth-next]");
    return shell ? shell.dataset.authNext || "/" : "/";
  };

  const prepareLogin = async (mediation) => {
    const optionsData = await postJSON("/auth/webauthn/login/options", { mediation });
    const assertion = optionsData.options || {};
    const publicKey = decodePublicKeyRequest(assertion.publicKey || {});

    return { optionsData, publicKey };
  };

  const startLogin = async (prepared, mediation, signal) => {
    const { optionsData, publicKey } = prepared;

    const requestOptions = { publicKey, signal };
    requestOptions.mediation = mediation;

    // Keep this call before the first await. Mobile Safari requires the standard
    // ceremony to begin during the button's transient user activation.
    const credential = await navigator.credentials.get(requestOptions);

    if (!credential) {
      throw new Error("no credential selected");
    }

    const verify = await postJSON("/auth/webauthn/login/verify", {
      challenge_id: optionsData.challenge_id,
      credential: credentialToJSON(credential),
      next: loginNext(),
    }, signal);

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
    let activeController = null;
    let activeMode = "";
    let preparedLogin = null;
    const preparedTask = prepareLogin("conditional").then((prepared) => {
      preparedLogin = prepared;
      return prepared;
    });
    button.disabled = true;

    const runLogin = async (mode, prepared) => {
      if (activeController) {
        return;
      }

      if (!window.PublicKeyCredential || !navigator.credentials) {
        showMessage("Passkeys are not supported in this browser.", true);
        return;
      }

      const controller = new AbortController();
      activeController = controller;
      activeMode = mode;

      if (mode === "required") {
        showMessage("", false);
        button.dataset.running = "true";
        button.disabled = true;
        button.setAttribute("aria-busy", "true");
      }

      try {
        await startLogin(prepared, mode, controller.signal);
      } catch (error) {
        const canceled = error && (error.name === "AbortError" || error.name === "NotAllowedError");
        if (mode !== "conditional" || !canceled) {
          console.warn("passkey login failed", error);
        }
        if (mode !== "conditional") {
          showMessage(authErrorMessage(error, "login"), true);
        }
      } finally {
        if (activeController === controller) {
          activeController = null;
          activeMode = "";
        }
        if (mode === "required") {
          button.dataset.running = "false";
          button.disabled = false;
          button.removeAttribute("aria-busy");
        }
      }
    };

    button.addEventListener("click", () => {
      if (!preparedLogin) {
        return;
      }
      if (activeController && activeMode === "conditional") {
        activeController.abort();
        activeController = null;
        activeMode = "";
      }
      void runLogin("required", preparedLogin);
    });

    if (typeof window.PublicKeyCredential.isConditionalMediationAvailable !== "function") {
      void preparedTask.then(() => {
        button.disabled = false;
      }).catch((error) => {
        button.disabled = false;
        console.warn("prepare passkey login failed", error);
        showMessage(authErrorMessage(error, "login"), true);
      });
      return;
    }

    void Promise.all([
      preparedTask,
      window.PublicKeyCredential.isConditionalMediationAvailable(),
    ]).then(([prepared, available]) => {
      button.disabled = false;
      if (!available || activeController) {
        return;
      }
      const selector = document.querySelector("[data-auth-passkey-selector]");
      if (selector) {
        selector.hidden = false;
      }
      void runLogin("conditional", prepared);
    }).catch((error) => {
      button.disabled = false;
      console.warn("prepare passkey login failed", error);
      showMessage(authErrorMessage(error, "login"), true);
    });
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
