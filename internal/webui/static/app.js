// LLM Subtitle Translator — Apple Design System Web UI App
document.addEventListener("DOMContentLoaded", () => {
  // State
  let uploadedFilePaths = [];
  let availableModels = [];
  let availableCurrencies = [];
  let allLanguages = [];
  let exchangeRatesMap = {};
  let modelPricingMap = {};
  let currentConfig = {};
  let currentPromptCost = 0.0;
  let currentCompletionCost = 0.0;
  let currentContextLength = 128000;
  let isTranslating = false;
  let sseEventSource = null;
  let highlightedModelIndex = -1;
  let highlightedCurrencyIndex = -1;

  // Elements
  const toastContainer = document.getElementById("toast-container");
  const activeProviderLabel = document.getElementById("active-provider-label");
  const activeModelLabel = document.getElementById("active-model-label");
  const rateInfoLabel = document.getElementById("rate-info-label");
  const themeToggleBtn = document.getElementById("theme-toggle");
  
  const uploadDropzone = document.getElementById("upload-dropzone");
  const dropzoneIcon = document.getElementById("dropzone-icon");
  const dropzoneText = document.getElementById("dropzone-text");
  const fileInput = document.getElementById("file-input");
  // Language Combos
  const sourceLangSearch = document.getElementById("source-lang-search");
  const sourceLangDropdown = document.getElementById("source-lang-dropdown");
  const targetLangSearch = document.getElementById("target-lang-search");
  const targetLangDropdown = document.getElementById("target-lang-dropdown");
  const fileList = document.getElementById("file-list");

  const modelSearch = document.getElementById("model-search");
  const modelDropdown = document.getElementById("model-dropdown");

  const currencySearch = document.getElementById("currency-search");
  const currencyDropdown = document.getElementById("currency-dropdown");

  const sourceLangSelect = document.getElementById("source-lang");
  const targetLangSelect = document.getElementById("target-lang");

  const excludedRangesInput = document.getElementById("excluded-ranges");
  const domainContextInput = document.getElementById("domain-context");

  const btnProject = document.getElementById("btn-project");
  const btnSaveSettings = document.getElementById("btn-save-settings");
  const btnStart = document.getElementById("btn-start");
  const btnCancel = document.getElementById("btn-cancel");
  const btnClearTerminal = document.getElementById("btn-clear-terminal");

  const terminalLogs = document.getElementById("terminal-logs");
  const terminalBody = document.getElementById("terminal-body");
  const downloadSection = document.getElementById("download-section");
  const downloadList = document.getElementById("download-list");

  // Modal Elements
  const modalBackdrop = document.getElementById("provider-modal-backdrop");
  const openProviderModalBtn = document.getElementById("open-provider-modal");
  const btnCloseModal = document.getElementById("btn-close-modal");
  const btnSaveProvider = document.getElementById("btn-save-provider");
  const modalProviderSelect = document.getElementById("modal-provider-select");
  const modalApiKey = document.getElementById("modal-api-key");
  const btnToggleKey = document.getElementById("btn-toggle-key");
  const modalLocalUrlGroup = document.getElementById("local-url-group");
  const modalLocalUrl = document.getElementById("modal-local-url");
  const thinkingEffortSelect = document.getElementById("thinking-effort");
  const contextModeSelect = document.getElementById("context-mode");
  const batchSizeInput = document.getElementById("batch-size");
  const contextLinesInput = document.getElementById("context-lines");

  // ---------------------------------------------------------------------------
  // Authentication
  // ---------------------------------------------------------------------------
  const nativeFetch = window.fetch.bind(window);

  function escapeHtml(value) {
    return String(value ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  async function apiFetch(url, options = {}) {
    const res = await nativeFetch(url, options);
    if (res.status === 401) {
      showLogin();
    }
    return res;
  }

  function showLogin() {
    const overlay = document.getElementById("login-overlay");
    if (!overlay) return;
    overlay.classList.remove("hidden");
    const input = document.getElementById("login-token-input");
    if (input) input.focus();
  }

  function hideLogin() {
    const overlay = document.getElementById("login-overlay");
    if (overlay) overlay.classList.add("hidden");
  }

  async function checkAuth() {
    try {
      const res = await nativeFetch("/api/config");
      return res.ok;
    } catch (e) {
      return false;
    }
  }

  async function loadAuthenticatedApp() {
    await fetchLanguages();
    await fetchConfiguration();
    await fetchModels();
    await fetchCurrencies();
    connectSSELogs();
  }

  async function doLogin() {
    const input = document.getElementById("login-token-input");
    const token = input ? input.value.trim() : "";
    if (!token) {
      showToast("Enter the access token.", "error");
      return;
    }
    try {
      const res = await nativeFetch("/api/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token }),
      });
      if (!res.ok) {
        showToast("Invalid access token.", "error");
        return;
      }
      if (input) input.value = "";
      hideLogin();
      await loadAuthenticatedApp();
    } catch (e) {
      showToast("Login failed.", "error");
    }
  }

  function setupAuthListeners() {
    const loginBtn = document.getElementById("btn-login");
    const input = document.getElementById("login-token-input");
    const logoutBtn = document.getElementById("btn-logout");
    if (loginBtn) loginBtn.addEventListener("click", doLogin);
    if (input) {
      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") doLogin();
      });
    }
    if (logoutBtn) {
      logoutBtn.addEventListener("click", async () => {
        try {
          await nativeFetch("/api/logout", { method: "POST" });
        } catch (e) {
          /* ignore */
        }
        showLogin();
      });
    }
  }

  setupAuthListeners();

  // Init Theme & App
  initTheme();
  init();

  function showToast(message, type = "info") {
    const toast = document.createElement("div");
    toast.className = `toast toast-${type}`;
    toast.textContent = message;
    toastContainer.appendChild(toast);

    setTimeout(() => {
      toast.classList.add("toast-fade-out");
      setTimeout(() => toast.remove(), 300);
    }, 4000);
  }

  function initTheme() {
    const savedTheme = localStorage.getItem("translator-theme") || "theme-dark";
    document.body.className = savedTheme;
    updateThemeToggleLabel(savedTheme);

    themeToggleBtn.addEventListener("click", () => {
      const current = document.body.classList.contains("theme-dark") ? "theme-dark" : "theme-light";
      const next = current === "theme-dark" ? "theme-light" : "theme-dark";
      document.body.className = next;
      localStorage.setItem("translator-theme", next);
      updateThemeToggleLabel(next);
    });
  }

  function updateThemeToggleLabel(theme) {
    if (theme === "theme-dark") {
      themeToggleBtn.innerHTML = '<span class="theme-icon">☀️</span><span class="theme-text"> Light Mode</span>';
    } else {
      themeToggleBtn.innerHTML = '<span class="theme-icon">🌙</span><span class="theme-text"> Dark Mode</span>';
    }
  }

  async function init() {
    setupEventListeners();
    appendLog("[Ready for translation request...]");
    if (!(await checkAuth())) {
      showLogin();
      return;
    }
    hideLogin();
    await loadAuthenticatedApp();
  }

  function setupEventListeners() {
    // Dropzone
    uploadDropzone.addEventListener("click", () => fileInput.click());
    fileInput.addEventListener("change", handleFileSelection);

    uploadDropzone.addEventListener("dragover", (e) => {
      e.preventDefault();
      uploadDropzone.classList.add("dragover");
    });
    uploadDropzone.addEventListener("dragleave", () => {
      uploadDropzone.classList.remove("dragover");
    });
    uploadDropzone.addEventListener("drop", (e) => {
      e.preventDefault();
      uploadDropzone.classList.remove("dragover");
      if (e.dataTransfer.files.length) {
        uploadFiles(e.dataTransfer.files);
      }
    });

    // Model Filterable Combo
    modelSearch.addEventListener("focus", () => showModelDropdown());
    modelSearch.addEventListener("input", () => {
      filterModels();
      updateModelPricingDisplay(modelSearch.value.trim());
    });

    modelSearch.addEventListener("keydown", (e) => {
      const items = modelDropdown.querySelectorAll(".dropdown-item");
      if (!items.length || modelDropdown.classList.contains("hidden")) {
        if (e.key === "ArrowDown") showModelDropdown();
        return;
      }

      if (e.key === "ArrowDown") {
        e.preventDefault();
        highlightedModelIndex = (highlightedModelIndex + 1) % items.length;
        updateDropdownHighlight(items, highlightedModelIndex);
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        highlightedModelIndex = (highlightedModelIndex - 1 + items.length) % items.length;
        updateDropdownHighlight(items, highlightedModelIndex);
      } else if (e.key === "Enter") {
        e.preventDefault();
        if (highlightedModelIndex >= 0 && highlightedModelIndex < items.length) {
          items[highlightedModelIndex].click();
        }
      } else if (e.key === "Escape") {
        modelDropdown.classList.add("hidden");
      }
    });

    // Currency Filterable Combo
    currencySearch.addEventListener("focus", () => showCurrencyDropdown());
    currencySearch.addEventListener("input", () => filterCurrencies());

    currencySearch.addEventListener("keydown", (e) => {
      const items = currencyDropdown.querySelectorAll(".dropdown-item");
      if (!items.length || currencyDropdown.classList.contains("hidden")) {
        if (e.key === "ArrowDown") showCurrencyDropdown();
        return;
      }

      if (e.key === "ArrowDown") {
        e.preventDefault();
        highlightedCurrencyIndex = (highlightedCurrencyIndex + 1) % items.length;
        updateDropdownHighlight(items, highlightedCurrencyIndex);
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        highlightedCurrencyIndex = (highlightedCurrencyIndex - 1 + items.length) % items.length;
        updateDropdownHighlight(items, highlightedCurrencyIndex);
      } else if (e.key === "Enter") {
        e.preventDefault();
        if (highlightedCurrencyIndex >= 0 && highlightedCurrencyIndex < items.length) {
          items[highlightedCurrencyIndex].click();
        }
      } else if (e.key === "Escape") {
        currencyDropdown.classList.add("hidden");
      }
    });

    // Source Language Custom Combo
    if (sourceLangSearch) {
      sourceLangSearch.addEventListener("focus", () => showSourceLangDropdown());
      sourceLangSearch.addEventListener("click", () => showSourceLangDropdown());
    }

    // Target Language Custom Combo
    if (targetLangSearch) {
      targetLangSearch.addEventListener("focus", () => showTargetLangDropdown());
      targetLangSearch.addEventListener("click", () => showTargetLangDropdown());
    }

    document.addEventListener("click", (e) => {
      const combo = e.target.closest(".filterable-combo");
      if (!combo) {
        closeAllDropdowns();
      } else {
        const currentList = combo.querySelector(".dropdown-list");
        closeAllDropdowns(currentList);
      }
    });

    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        closeAllDropdowns();
      }
    });

    // Actions
    btnProject.addEventListener("click", handleProject);
    btnSaveSettings.addEventListener("click", handleSaveSettings);
    btnStart.addEventListener("click", handleStartTranslation);
    btnCancel.addEventListener("click", handleCancelTranslation);
    btnClearTerminal.addEventListener("click", () => {
      terminalLogs.textContent = "";
    });

    // Modal
    openProviderModalBtn.addEventListener("click", () => {
      syncModalApiKeyField();
      modalBackdrop.classList.remove("hidden");
    });
    btnCloseModal.addEventListener("click", () => modalBackdrop.classList.add("hidden"));
    btnSaveProvider.addEventListener("click", handleSaveProvider);

    modalProviderSelect.addEventListener("change", () => {
      syncModalApiKeyField();
    });

    btnToggleKey.addEventListener("click", () => {
      modalApiKey.type = modalApiKey.type === "password" ? "text" : "password";
    });
  }

  function updateDropdownHighlight(items, selectedIndex) {
    items.forEach((item, index) => {
      if (index === selectedIndex) {
        item.classList.add("highlighted");
        item.scrollIntoView({ block: "nearest" });
      } else {
        item.classList.remove("highlighted");
      }
    });
  }

  function apiKeyFieldName(provider) {
    if (provider === "OpenCode Zen" || provider === "OpenCode Go") {
      return "opencode_api_key";
    }
    return `${provider.toLowerCase().replace(" ", "_")}_api_key`;
  }

  function syncModalApiKeyField() {
    const provider = modalProviderSelect.value;
    const keyName = apiKeyFieldName(provider);
    const savedKey = currentConfig[keyName] || "";
    modalApiKey.value = savedKey;
    modalApiKey.type = "password";

    if (modalLocalUrlGroup) {
      modalLocalUrlGroup.classList.toggle("hidden", provider !== "Local");
    }
    if (modalLocalUrl && provider === "Local") {
      modalLocalUrl.value = currentConfig.local_server_url || "";
    }
  }

  function closeAllDropdowns(exceptElement = null) {
    document.querySelectorAll(".dropdown-list").forEach((list) => {
      if (list !== exceptElement) {
        list.classList.add("hidden");
      }
    });
  }

  function positionDropdown(inputElement, dropdownElement) {
    if (!inputElement || !dropdownElement) return;
    const rect = inputElement.getBoundingClientRect();
    const spaceBelow = window.innerHeight - rect.bottom;
    const cardElement = inputElement.closest(".settings-card, .store-utility-card");
    let spaceToCardBottom = 9999;
    if (cardElement) {
      const cardRect = cardElement.getBoundingClientRect();
      spaceToCardBottom = cardRect.bottom - rect.bottom;
    }

    if (spaceBelow < 210 || spaceToCardBottom < 210) {
      dropdownElement.classList.add("dropup");
    } else {
      dropdownElement.classList.remove("dropup");
    }
  }

  async function fetchLanguages() {
    try {
      const res = await apiFetch("/api/languages");
      const data = await res.json();
      let rawLangs = data.languages;

      if (Array.isArray(rawLangs)) {
        allLanguages = rawLangs.map((item) =>
          typeof item === "string" ? { code: item, name: item } : item
        );
      } else if (typeof rawLangs === "object" && rawLangs !== null) {
        allLanguages = Object.entries(rawLangs).map(([code, name]) => ({
          code: code,
          name: name,
        }));
      } else {
        allLanguages = [
          { code: "EN", name: "English" },
          { code: "ES", name: "Spanish" },
          { code: "JA", name: "Japanese" },
          { code: "ZH", name: "Chinese" },
          { code: "KO", name: "Korean" },
          { code: "FR", name: "French" },
          { code: "DE", name: "German" },
          { code: "PT", name: "Portuguese" },
          { code: "IT", name: "Italian" },
          { code: "RU", name: "Russian" },
          { code: "TR", name: "Turkish" },
          { code: "AR", name: "Arabic" },
        ];
      }

      if (sourceLangSelect) {
        sourceLangSelect.innerHTML = "";
        allLanguages.forEach((langObj) => {
          const optionText = langObj.name !== langObj.code ? `${langObj.code} - ${langObj.name}` : langObj.code;
          sourceLangSelect.add(new Option(optionText, langObj.code));
        });

        const initialSource = currentConfig.source_lang || "EN";
        if (allLanguages.some((l) => l.code === initialSource)) {
          sourceLangSelect.value = initialSource;
        }
        const initialObj = allLanguages.find((l) => l.code === sourceLangSelect.value);
        if (initialObj && sourceLangSearch) {
          sourceLangSearch.value = initialObj.name !== initialObj.code ? `${initialObj.code} - ${initialObj.name}` : initialObj.code;
        }
      }

      renderSourceLangDropdown();
      renderTargetLangDropdown(currentConfig.target_lang || "ES");
    } catch (e) {
      console.error("Failed to fetch languages:", e);
    }
  }

  function renderSourceLangDropdown() {
    if (!sourceLangDropdown) return;
    sourceLangDropdown.innerHTML = "";
    allLanguages.forEach((langObj) => {
      const li = document.createElement("li");
      li.className = "dropdown-item";
      const optionText = langObj.name !== langObj.code ? `${langObj.code} - ${langObj.name}` : langObj.code;
      li.textContent = optionText;
      li.addEventListener("click", () => {
        if (sourceLangSelect) sourceLangSelect.value = langObj.code;
        if (sourceLangSearch) sourceLangSearch.value = optionText;
        sourceLangDropdown.classList.add("hidden");
        renderTargetLangDropdown(targetLangSelect ? targetLangSelect.value : "ES");
        updateModelPricingDisplay(modelSearch.value.trim());
      });
      sourceLangDropdown.appendChild(li);
    });
  }

  function renderTargetLangDropdown(preferredTarget = null) {
    if (!targetLangDropdown) return;
    const currentSource = sourceLangSelect ? sourceLangSelect.value : "EN";
    targetLangDropdown.innerHTML = "";

    const availableTargets = allLanguages.filter((l) => l.code !== currentSource);
    availableTargets.forEach((langObj) => {
      const li = document.createElement("li");
      li.className = "dropdown-item";
      const optionText = langObj.name !== langObj.code ? `${langObj.code} - ${langObj.name}` : langObj.code;
      li.textContent = optionText;
      li.addEventListener("click", () => {
        if (targetLangSelect) targetLangSelect.value = langObj.code;
        if (targetLangSearch) targetLangSearch.value = optionText;
        targetLangDropdown.classList.add("hidden");
      });
      targetLangDropdown.appendChild(li);
    });

    if (targetLangSelect) {
      targetLangSelect.innerHTML = "";
      availableTargets.forEach((langObj) => {
        const optionText = langObj.name !== langObj.code ? `${langObj.code} - ${langObj.name}` : langObj.code;
        targetLangSelect.add(new Option(optionText, langObj.code));
      });
    }

    const targetCodes = availableTargets.map((l) => l.code);
    let selectedCode = preferredTarget || (targetLangSelect ? targetLangSelect.value : "ES");
    if (!targetCodes.includes(selectedCode)) {
      selectedCode = targetCodes.includes("ES") ? "ES" : (targetCodes[0] ? targetCodes[0] : "ES");
    }
    if (targetLangSelect) targetLangSelect.value = selectedCode;
    const activeObj = allLanguages.find((l) => l.code === selectedCode);
    if (activeObj && targetLangSearch) {
      targetLangSearch.value = activeObj.name !== activeObj.code ? `${activeObj.code} - ${activeObj.name}` : activeObj.code;
    }
  }

  function updateTargetLanguageOptions(preferredTarget = null) {
    renderTargetLangDropdown(preferredTarget);
  }

  async function fetchConfiguration() {
    try {
      const res = await apiFetch("/api/config");
      const cfg = await res.json();
      currentConfig = cfg || {};
      if (cfg.active_provider) {
        activeProviderLabel.textContent = `Provider: ${cfg.active_provider}`;
        modalProviderSelect.value = cfg.active_provider;
      }
      if (cfg.model_id) {
        modelSearch.value = cfg.model_id;
        activeModelLabel.textContent = `Model: ${cfg.model_id}`;
      }
      if (cfg.source_lang && sourceLangSelect.options.length > 0) {
        sourceLangSelect.value = cfg.source_lang;
      }
      updateTargetLanguageOptions(cfg.target_lang || "ES");
      if (cfg.target_currency) currencySearch.value = cfg.target_currency;
      if (cfg.domain_context) domainContextInput.value = cfg.domain_context;
      if (cfg.reasoning_effort && thinkingEffortSelect) thinkingEffortSelect.value = cfg.reasoning_effort;
      if (cfg.context_mode && contextModeSelect) contextModeSelect.value = cfg.context_mode;
      if (cfg.batch_size && batchSizeInput) batchSizeInput.value = cfg.batch_size;
      if (cfg.context_lines && contextLinesInput) contextLinesInput.value = cfg.context_lines;
      syncModalApiKeyField();
    } catch (e) {
      console.error("Failed to fetch configuration:", e);
    }
  }

  async function fetchModels() {
    try {
      const res = await apiFetch(`/api/models?provider=${encodeURIComponent(modalProviderSelect.value)}`);
      const data = await res.json();
      availableModels = data.models || [];
      modelPricingMap = data.pricing || {};
      renderModelDropdown(availableModels);
      if (modelSearch.value) {
        updateModelPricingDisplay(modelSearch.value);
      }
    } catch (e) {
      console.error("Failed to fetch models:", e);
    }
  }

  async function fetchCurrencies() {
    try {
      const res = await apiFetch("/api/currencies");
      const data = await res.json();
      availableCurrencies = data.currencies || ["USD", "ARS", "EUR", "GBP", "BRL"];
      exchangeRatesMap = data.rates || {};
      renderCurrencyDropdown(availableCurrencies);
      if (modelSearch.value) {
        updateModelPricingDisplay(modelSearch.value.trim());
      }
    } catch (e) {
      console.error("Failed to fetch currencies:", e);
    }
  }

  function updateModelPricingDisplay(modelId) {
    if (!modelId) {
      activeModelLabel.textContent = "Model: (none)";
      rateInfoLabel.textContent = "Rate: $0 / 1M tokens";
      currentPromptCost = 0.0;
      currentCompletionCost = 0.0;
      return;
    }

    activeModelLabel.textContent = `Model: ${modelId}`;

    let matchedEndpoint = null;
    if (selectedProvider && rawLoadedEndpoints && rawLoadedEndpoints.length) {
      const lowerProv = selectedProvider.toLowerCase();
      matchedEndpoint = rawLoadedEndpoints.find((ep) => {
        const name = (ep.provider_name || ep.name || "").toLowerCase();
        return name === lowerProv;
      });
    }

    if (!matchedEndpoint && rawLoadedEndpoints && rawLoadedEndpoints.length) {
      const sorted = [...rawLoadedEndpoints].sort((a, b) => {
        const pA = a.pricing?.prompt ? parseFloat(a.pricing.prompt) : Infinity;
        const pB = b.pricing?.prompt ? parseFloat(b.pricing.prompt) : Infinity;
        return pA - pB;
      });
      if (sorted.length > 0) {
        matchedEndpoint = sorted[0];
      }
    }

    if (matchedEndpoint && matchedEndpoint.pricing) {
      if (matchedEndpoint.pricing.prompt !== undefined && matchedEndpoint.pricing.prompt !== null) {
        currentPromptCost = parseFloat(matchedEndpoint.pricing.prompt);
      }
      if (matchedEndpoint.pricing.completion !== undefined && matchedEndpoint.pricing.completion !== null) {
        currentCompletionCost = parseFloat(matchedEndpoint.pricing.completion);
      }
      if (matchedEndpoint.context_length) {
        currentContextLength = matchedEndpoint.context_length;
      }
    } else {
      let pInfo = modelPricingMap[modelId];
      if (!pInfo) {
        const lowerModelId = modelId.toLowerCase();
        const base = modelId.split("/").pop().toLowerCase();
        for (const k in modelPricingMap) {
          const lowerK = k.toLowerCase();
          const baseK = k.split("/").pop().toLowerCase();
          if (lowerK === lowerModelId || baseK === base || lowerK.endsWith("/" + base)) {
            pInfo = modelPricingMap[k];
            break;
          }
        }
      }

      if (pInfo) {
        currentPromptCost = pInfo.prompt_cost !== undefined ? pInfo.prompt_cost : (pInfo.prompt !== undefined ? pInfo.prompt : 0.0);
        currentCompletionCost = pInfo.completion_cost !== undefined ? pInfo.completion_cost : (pInfo.completion !== undefined ? pInfo.completion : 0.0);
        currentContextLength = pInfo.context_length || 128000;
      } else {
        currentPromptCost = 0.0;
        currentCompletionCost = 0.0;
      }
    }

    const p1m = currentPromptCost * 1000000;
    const c1m = currentCompletionCost * 1000000;

    if (p1m > 0 || c1m > 0) {
      const targetCurr = (currencySearch.value || "USD").trim().toUpperCase();
      const rate = exchangeRatesMap[targetCurr];

      if (rate && targetCurr !== "USD") {
        const p1m_local = p1m * rate;
        const c1m_local = c1m * rate;
        rateInfoLabel.textContent = `Rate: In $${p1m.toFixed(4)} USD ($${p1m_local.toFixed(2)} ${targetCurr}) | Out $${c1m.toFixed(4)} USD ($${c1m_local.toFixed(2)} ${targetCurr}) / 1M tokens`;
      } else {
        rateInfoLabel.textContent = `Rate: In $${p1m.toFixed(4)} | Out $${c1m.toFixed(4)} USD / 1M tokens`;
      }
    } else {
      rateInfoLabel.textContent = `Rate: $0 (free/local)`;
    }

    if (activeProviderLabel) {
      const mainProv = currentConfig.active_provider || "OpenRouter";
      if (selectedProvider) {
        activeProviderLabel.textContent = `Provider: ${mainProv} (${selectedProvider})`;
      } else {
        activeProviderLabel.textContent = `Provider: ${mainProv}`;
      }
    }
  }

  function renderModelDropdown(models) {
    modelDropdown.innerHTML = "";
    highlightedModelIndex = -1;
    models.forEach((m) => {
      const li = document.createElement("li");
      li.className = "dropdown-item";
      li.textContent = m;
      li.addEventListener("click", () => {
        modelSearch.value = m;
        updateModelPricingDisplay(m);
        modelDropdown.classList.add("hidden");
      });
      modelDropdown.appendChild(li);
    });
  }

  function filterModels() {
    const q = modelSearch.value.toLowerCase();
    const filtered = availableModels.filter((m) => m.toLowerCase().includes(q));
    renderModelDropdown(filtered);
    showModelDropdown();
  }

  function showModelDropdown() {
    if (availableModels.length > 0) {
      closeAllDropdowns(modelDropdown);
      positionDropdown(modelSearch, modelDropdown);
      modelDropdown.classList.remove("hidden");
    }
  }

  function renderCurrencyDropdown(currencies) {
    currencyDropdown.innerHTML = "";
    highlightedCurrencyIndex = -1;
    currencies.forEach((c) => {
      const li = document.createElement("li");
      li.className = "dropdown-item";
      li.textContent = c;
      li.addEventListener("click", () => {
        currencySearch.value = c;
        currencyDropdown.classList.add("hidden");
        updateModelPricingDisplay(modelSearch.value.trim());
      });
      currencyDropdown.appendChild(li);
    });
  }

  function filterCurrencies() {
    const q = currencySearch.value.trim().toUpperCase();
    const filtered = availableCurrencies.filter((c) => c.includes(q));
    renderCurrencyDropdown(filtered);
    showCurrencyDropdown();
  }

  function showCurrencyDropdown() {
    if (availableCurrencies.length > 0) {
      closeAllDropdowns(currencyDropdown);
      positionDropdown(currencySearch, currencyDropdown);
      currencyDropdown.classList.remove("hidden");
    }
  }

  function showSourceLangDropdown() {
    if (sourceLangDropdown) {
      closeAllDropdowns(sourceLangDropdown);
      positionDropdown(sourceLangSearch, sourceLangDropdown);
      sourceLangDropdown.classList.remove("hidden");
    }
  }

  function showTargetLangDropdown() {
    if (targetLangDropdown) {
      closeAllDropdowns(targetLangDropdown);
      positionDropdown(targetLangSearch, targetLangDropdown);
      targetLangDropdown.classList.remove("hidden");
    }
  }

  async function handleFileSelection(e) {
    if (e.target.files.length) {
      await uploadFiles(e.target.files);
    }
  }

  async function uploadFiles(files) {
    const formData = new FormData();
    for (let i = 0; i < files.length; i++) {
      formData.append("files", files[i]);
    }

    uploadDropzone.classList.add("uploading");
    dropzoneIcon.classList.add("spinner");
    dropzoneIcon.textContent = "⏳";
    dropzoneText.innerHTML = `<strong>Uploading ${files.length} file(s)...</strong><span>Please wait</span>`;

    try {
      appendLog(`[Upload] Uploading ${files.length} file(s)...`);
      const res = await apiFetch("/api/upload", { method: "POST", body: formData });
      const data = await res.json();
      
      data.uploaded_files.forEach((file) => {
        if (!uploadedFilePaths.includes(file.filepath)) {
          uploadedFilePaths.push(file.filepath);
          renderFileList(file.filename, file.filepath);
        }
      });
      appendLog(`[Upload] ${data.uploaded_files.length} file(s) ready.`);
      showToast(`Uploaded ${data.uploaded_files.length} file(s) successfully.`, "success");
    } catch (e) {
      appendLog(`[Error] File upload failed: ${e}`);
      showToast("File upload failed.", "error");
    } finally {
      uploadDropzone.classList.remove("uploading");
      dropzoneIcon.classList.remove("spinner");
      dropzoneIcon.textContent = "📁";
      dropzoneText.innerHTML = `<strong>Click or drag subtitle file(s) here</strong><span>Supports .srt, .ass, .ssa, .vtt</span>`;
    }
  }

  function renderFileList(filename, filepath) {
    const cleanName = filename.replace(/^[0-9a-fA-F]{8}_/, "");
    const li = document.createElement("li");
    li.className = "file-item";
    li.innerHTML = `
      <span>📁 ${escapeHtml(cleanName)}</span>
      <span class="remove-btn">✕</span>
    `;
    li.querySelector(".remove-btn").addEventListener("click", () => {
      uploadedFilePaths = uploadedFilePaths.filter((p) => p !== filepath);
      li.remove();
    });
    fileList.appendChild(li);
  }

  function connectSSELogs() {
    if (sseEventSource) sseEventSource.close();
    sseEventSource = new EventSource("/api/stream-logs");

    sseEventSource.onmessage = (event) => {
      if (event.data) {
        appendLog(event.data);
        if (event.data.includes("=== TRANSLATION COMPLETED ===")) {
          onTranslationComplete();
        } else if (event.data.includes("=== TRANSLATION CANCELLED ===")) {
          setTranslatingUI(false);
          showToast("Translation cancelled.", "info");
        }
      }
    };
  }

  function appendLog(text) {
    if (!text) return;
    if (terminalLogs.textContent.length > 0 && !terminalLogs.textContent.endsWith("\n")) {
      terminalLogs.textContent += "\n";
    }
    terminalLogs.textContent += text;
    terminalBody.scrollTop = terminalBody.scrollHeight;
  }

  async function handleProject() {
    if (!uploadedFilePaths.length) {
      showToast("Select at least one subtitle file first.", "error");
      return;
    }
    const payload = getPayload();
    try {
      appendLog("\n=== CALCULATING FINANCIAL PROJECTION ===");
      const res = await apiFetch("/api/project", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await res.json();
      if (data.status === "success") {
        if (data.prompt_cost || data.completion_cost) {
          currentPromptCost = data.prompt_cost;
          currentCompletionCost = data.completion_cost;
          updateModelPricingDisplay(modelSearch.value);
        }
        showToast("Financial projection calculated.", "success");
      }
    } catch (e) {
      appendLog(`[Error] Projection failed: ${e}`);
      showToast("Financial projection failed.", "error");
    }
  }

  async function handleStartTranslation() {
    if (!uploadedFilePaths.length) {
      showToast("Select at least one subtitle file first.", "error");
      return;
    }
    if (!modelSearch.value) {
      showToast("Select an LLM model first.", "error");
      return;
    }

    const payload = getPayload();
    setTranslatingUI(true);

    try {
      const res = await apiFetch("/api/translate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await res.json();
      if (data.status !== "started") {
        showToast(data.detail || "Could not start translation.", "error");
        setTranslatingUI(false);
      } else {
        showToast("Translation session started.", "info");
      }
    } catch (e) {
      appendLog(`[Error] Failed to start translation: ${e}`);
      showToast("Failed to start translation.", "error");
      setTranslatingUI(false);
    }
  }

  async function handleCancelTranslation() {
    try {
      btnCancel.disabled = true;
      await apiFetch("/api/cancel", { method: "POST" });
      appendLog("\n[CANCEL] Cancellation request sent...\n");
      setTranslatingUI(false);
      showToast("Translation cancelled.", "info");
    } catch (e) {
      console.error(e);
    } finally {
      btnCancel.disabled = false;
    }
  }

  function setTranslatingUI(running) {
    isTranslating = running;
    btnStart.classList.toggle("hidden", running);
    btnCancel.classList.toggle("hidden", !running);
    btnProject.disabled = running;
    btnSaveSettings.disabled = running;
  }

  function onTranslationComplete() {
    setTranslatingUI(false);
    renderDownloads();
    showToast("Translation completed!", "success");
  }

  function renderDownloads() {
    downloadSection.classList.remove("hidden");
    downloadList.innerHTML = "";
    uploadedFilePaths.forEach((path) => {
      const fullFilename = path.split("/").pop();
      const extIndex = fullFilename.lastIndexOf(".");
      const baseName = extIndex !== -1 ? fullFilename.substring(0, extIndex) : fullFilename;
      const ext = extIndex !== -1 ? fullFilename.substring(extIndex) : "";
      const targetLang = (targetLangSelect.value || "es").toLowerCase().substring(0, 2);

      const diskFilename = `${baseName}_${targetLang}${ext}`;
      const cleanDisplayName = diskFilename.replace(/^[0-9a-fA-F]{8}_/, "");

      const div = document.createElement("div");
      div.className = "download-item";

      const btn = document.createElement("button");
      btn.className = "download-link";
      btn.textContent = "📥 Download";
      btn.addEventListener("click", async () => {
        btn.textContent = "⏳ Downloading...";
        btn.disabled = true;
        try {
          const res = await apiFetch(`/api/download/${encodeURIComponent(diskFilename)}`);
          if (!res.ok) throw new Error(`Server error: ${res.status}`);
          const blob = await res.blob();
          const url = URL.createObjectURL(blob);
          const a = document.createElement("a");
          a.href = url;
          a.download = cleanDisplayName;
          document.body.appendChild(a);
          a.click();
          document.body.removeChild(a);
          URL.revokeObjectURL(url);
          btn.textContent = "✅ Downloaded";
        } catch (e) {
          btn.textContent = "❌ Failed";
          showToast(`Download failed: ${e.message}`, "error");
        } finally {
          setTimeout(() => {
            btn.textContent = "📥 Download";
            btn.disabled = false;
          }, 3000);
        }
      });

      const span = document.createElement("span");
      span.textContent = `📄 ${cleanDisplayName}`;

      div.appendChild(span);
      div.appendChild(btn);
      downloadList.appendChild(div);
    });
  }

  async function handleSaveSettings() {
    const srcLang = sourceLangSelect.value;
    const tgtLang = targetLangSelect.value;

    if (!srcLang || !tgtLang) {
      showToast("Please select valid Source and Target languages before saving.", "error");
      return;
    }

    if (!modelSearch.value) {
      showToast("Please select an LLM model before saving.", "error");
      return;
    }

    const payload = {
      model_id: modelSearch.value,
      source_lang: srcLang,
      target_lang: tgtLang,
      target_currency: currencySearch.value.trim().toUpperCase() || "USD",
      domain_context: domainContextInput.value,
      excluded_ranges: excludedRangesInput.value,
      prompt_cost: currentPromptCost,
      completion_cost: currentCompletionCost,
      context_length: currentContextLength,
      reasoning_effort: thinkingEffortSelect ? thinkingEffortSelect.value : "auto",
      context_mode: contextModeSelect ? contextModeSelect.value : "overlap",
      batch_size: batchSizeInput ? (parseInt(batchSizeInput.value, 10) || 100) : 100,
      context_lines: contextLinesInput ? (parseInt(contextLinesInput.value, 10) || 5) : 5,
    };
    try {
      const res = await apiFetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (res.ok) {
        appendLog("[Settings] Configuration saved.");
        showToast("Settings saved to translator_config.json", "success");
      } else {
        showToast("Failed to save settings.", "error");
      }
    } catch (e) {
      showToast("Failed to save settings.", "error");
    }
  }

  async function handleSaveProvider() {
    const provider = modalProviderSelect.value;
    const apiKey = modalApiKey.value.trim();
    const keyName = apiKeyFieldName(provider);

    const payload = {
      active_provider: provider,
      [keyName]: apiKey,
    };
    if (provider === "Local" && modalLocalUrl) {
      payload.local_server_url = modalLocalUrl.value.trim();
    }

    try {
      const res = await apiFetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await res.json();
      currentConfig = data.config || currentConfig;
      if (apiKey && apiKey !== "••••••••") {
        currentConfig[keyName] = apiKey;
      }

      activeProviderLabel.textContent = `Provider: ${provider}`;
      modalBackdrop.classList.add("hidden");
      await fetchModels();
      appendLog(`[Provider] Switched to ${provider}.`);
      showToast(`Provider set to ${provider}`, "success");
    } catch (e) {
      showToast("Failed to save provider.", "error");
    }
  }

  let selectedProvider = "";
  let rawLoadedEndpoints = [];
  let sortStack = [{ col: "price_prompt", dir: "asc" }];
  let thresholdFilters = {
    minUptime: null,
    maxPromptPrice: null,
    maxCompletionPrice: null,
  };

  function updateProviderBadge() {
    const routingBadge = document.getElementById("current-routing-badge");
    const btnReset = document.getElementById("btn-reset-provider");
    if (routingBadge) {
      if (selectedProvider) {
        routingBadge.textContent = `Pinned to ${selectedProvider}`;
        routingBadge.style.color = "#4cd964";
        if (btnReset) btnReset.classList.remove("hidden");
      } else {
        routingBadge.textContent = "Auto (OpenRouter Optimization)";
        routingBadge.style.color = "#4cd964";
        if (btnReset) btnReset.classList.add("hidden");
      }
    }
  }

  function getPayload() {
    const providerSortSelect = document.getElementById("provider-sort") || document.getElementById("modal-sort-select");
    const allowTrainingCheckbox = document.getElementById("allow-training");

    let sortVal = "price";
    if (providerSortSelect) {
      const rawVal = providerSortSelect.value;
      if (rawVal.includes("latency")) sortVal = "latency";
      else sortVal = "price";
    }

    return {
      file_paths: uploadedFilePaths,
      model_id: modelSearch.value,
      source_lang: sourceLangSelect.value,
      target_lang: targetLangSelect.value,
      target_currency: currencySearch.value.trim().toUpperCase() || "USD",
      domain_context: domainContextInput.value,
      excluded_ranges: excludedRangesInput.value,
      prompt_cost: currentPromptCost,
      completion_cost: currentCompletionCost,
      context_length: currentContextLength,
      provider_sort: sortVal,
      allow_training: allowTrainingCheckbox ? allowTrainingCheckbox.checked : true,
      selected_provider: selectedProvider,
      reasoning_effort: thinkingEffortSelect ? thinkingEffortSelect.value : "auto",
      context_mode: contextModeSelect ? contextModeSelect.value : "overlap",
      batch_size: batchSizeInput ? (parseInt(batchSizeInput.value, 10) || 100) : 100,
      context_lines: contextLinesInput ? (parseInt(contextLinesInput.value, 10) || 5) : 5,
      model_supports_reasoning: !!(modelPricingMap[modelSearch.value] && modelPricingMap[modelSearch.value].reasoning),
    };
  }

  // Model Endpoints Modal Handlers
  const btnViewEndpoints = document.getElementById("btn-view-endpoints");
  const endpointsModalBackdrop = document.getElementById("endpoints-modal-backdrop");
  const btnCloseEndpointsModal = document.getElementById("btn-close-endpoints-modal");
  const endpointsTableContainer = document.getElementById("endpoints-table-container");
  const modalProviderFilter = document.getElementById("modal-provider-filter");
  const modalSortSelect = document.getElementById("modal-sort-select");
  const btnResetProvider = document.getElementById("btn-reset-provider");
  const filterMinUptimeInput = document.getElementById("filter-min-uptime");
  const filterMaxPromptInput = document.getElementById("filter-max-prompt");
  const filterMaxCompletionInput = document.getElementById("filter-max-completion");
  const btnClearThresholds = document.getElementById("btn-clear-thresholds");
  const btnResetAllFilters = document.getElementById("btn-reset-all-filters");

  if (btnResetProvider) {
    btnResetProvider.addEventListener("click", () => {
      selectedProvider = "";
      updateProviderBadge();
      updateModelPricingDisplay(modelSearch ? modelSearch.value.trim() : "");
      renderEndpointsTable();
      showToast("Provider routing reset to Auto mode.", "info");
    });
  }

  if (filterMinUptimeInput) {
    filterMinUptimeInput.addEventListener("input", () => {
      const val = parseFloat(filterMinUptimeInput.value);
      thresholdFilters.minUptime = !isNaN(val) ? val : null;
      renderEndpointsTable();
    });
  }

  if (filterMaxPromptInput) {
    filterMaxPromptInput.addEventListener("input", () => {
      const val = parseFloat(filterMaxPromptInput.value);
      thresholdFilters.maxPromptPrice = !isNaN(val) ? val : null;
      renderEndpointsTable();
    });
  }

  if (filterMaxCompletionInput) {
    filterMaxCompletionInput.addEventListener("input", () => {
      const val = parseFloat(filterMaxCompletionInput.value);
      thresholdFilters.maxCompletionPrice = !isNaN(val) ? val : null;
      renderEndpointsTable();
    });
  }

  if (btnClearThresholds) {
    btnClearThresholds.addEventListener("click", () => {
      thresholdFilters.minUptime = null;
      thresholdFilters.maxPromptPrice = null;
      thresholdFilters.maxCompletionPrice = null;
      if (filterMinUptimeInput) filterMinUptimeInput.value = "";
      if (filterMaxPromptInput) filterMaxPromptInput.value = "";
      if (filterMaxCompletionInput) filterMaxCompletionInput.value = "";
      renderEndpointsTable();
    });
  }

  if (btnResetAllFilters) {
    btnResetAllFilters.addEventListener("click", () => {
      thresholdFilters.minUptime = null;
      thresholdFilters.maxPromptPrice = null;
      thresholdFilters.maxCompletionPrice = null;
      if (filterMinUptimeInput) filterMinUptimeInput.value = "";
      if (filterMaxPromptInput) filterMaxPromptInput.value = "";
      if (filterMaxCompletionInput) filterMaxCompletionInput.value = "";
      if (modalProviderFilter) modalProviderFilter.value = "";
      sortStack = [{ col: "price_prompt", dir: "asc" }];
      syncSortDropdownSelect();
      renderEndpointsTable();
      showToast("All filters and sorts reset.", "info");
    });
  }

  function syncSortDropdownSelect() {
    if (!modalSortSelect) return;
    if (sortStack.length === 1) {
      const { col, dir } = sortStack[0];
      let targetVal = "";
      if (col === "price_prompt") targetVal = dir === "asc" ? "price_asc" : "price_desc";
      else if (col === "price_completion") targetVal = dir === "asc" ? "completion_asc" : "completion_desc";
      else if (col === "uptime") targetVal = dir === "desc" ? "uptime_desc" : "uptime_asc";
      else if (col === "provider") targetVal = dir === "asc" ? "provider_asc" : "provider_desc";
      else if (col === "quantization") targetVal = dir === "asc" ? "quant_asc" : "quant_desc";

      if (targetVal) modalSortSelect.value = targetVal;
    } else if (sortStack.length > 1) {
      let multiOpt = modalSortSelect.querySelector("option[value='multi']");
      if (!multiOpt) {
        multiOpt = document.createElement("option");
        multiOpt.value = "multi";
        modalSortSelect.appendChild(multiOpt);
      }
      multiOpt.textContent = `Multi-Sort (${sortStack.length} criteria active)`;
      modalSortSelect.value = "multi";
    }
  }

  function renderActiveFilterChips() {
    const chipsContainer = document.getElementById("active-filter-chips");
    if (!chipsContainer) return;
    chipsContainer.innerHTML = "";

    const colNames = {
      provider: "Provider",
      price_prompt: "Prompt (1M)",
      price_completion: "Completion (1M)",
      quantization: "Quantization",
      uptime: "Uptime",
    };

    // Sort Stack Chips
    sortStack.forEach((item, index) => {
      const chip = document.createElement("span");
      chip.className = "filter-chip";
      const name = colNames[item.col] || item.col;
      const arrow = item.dir === "asc" ? "▲" : "▼";
      chip.innerHTML = `<span>Filter ${index + 1}: ${name} ${arrow}</span><span class="chip-remove" data-remove-sort="${index}">✕</span>`;
      chip.querySelector("[data-remove-sort]").addEventListener("click", (e) => {
        e.stopPropagation();
        if (sortStack.length > 1) {
          sortStack.splice(index, 1);
        } else {
          sortStack = [{ col: "price_prompt", dir: "asc" }];
        }
        syncSortDropdownSelect();
        renderEndpointsTable();
      });
      chipsContainer.appendChild(chip);
    });

    // Threshold Chips
    if (thresholdFilters.minUptime !== null && !isNaN(thresholdFilters.minUptime)) {
      const chip = document.createElement("span");
      chip.className = "filter-chip";
      chip.innerHTML = `<span>Uptime ≥ ${thresholdFilters.minUptime}%</span><span class="chip-remove">✕</span>`;
      chip.querySelector(".chip-remove").addEventListener("click", () => {
        thresholdFilters.minUptime = null;
        if (filterMinUptimeInput) filterMinUptimeInput.value = "";
        renderEndpointsTable();
      });
      chipsContainer.appendChild(chip);
    }

    if (thresholdFilters.maxPromptPrice !== null && !isNaN(thresholdFilters.maxPromptPrice)) {
      const chip = document.createElement("span");
      chip.className = "filter-chip";
      chip.innerHTML = `<span>Prompt ≤ $${thresholdFilters.maxPromptPrice}/1M</span><span class="chip-remove">✕</span>`;
      chip.querySelector(".chip-remove").addEventListener("click", () => {
        thresholdFilters.maxPromptPrice = null;
        if (filterMaxPromptInput) filterMaxPromptInput.value = "";
        renderEndpointsTable();
      });
      chipsContainer.appendChild(chip);
    }

    if (thresholdFilters.maxCompletionPrice !== null && !isNaN(thresholdFilters.maxCompletionPrice)) {
      const chip = document.createElement("span");
      chip.className = "filter-chip";
      chip.innerHTML = `<span>Completion ≤ $${thresholdFilters.maxCompletionPrice}/1M</span><span class="chip-remove">✕</span>`;
      chip.querySelector(".chip-remove").addEventListener("click", () => {
        thresholdFilters.maxCompletionPrice = null;
        if (filterMaxCompletionInput) filterMaxCompletionInput.value = "";
        renderEndpointsTable();
      });
      chipsContainer.appendChild(chip);
    }
  }

  function getUptimeVal(e) {
    if (!e) return null;
    if (e.uptime_last_5m !== undefined && e.uptime_last_5m !== null) return parseFloat(e.uptime_last_5m);
    if (e.uptime_last_30m !== undefined && e.uptime_last_30m !== null) return parseFloat(e.uptime_last_30m);
    if (e.uptime_last_1d !== undefined && e.uptime_last_1d !== null) return parseFloat(e.uptime_last_1d);
    if (e.uptime !== undefined && e.uptime !== null) return parseFloat(e.uptime);
    return null;
  }

  function renderEndpointsTable() {
    if (!endpointsTableContainer) return;
    renderActiveFilterChips();

    if (!rawLoadedEndpoints || !rawLoadedEndpoints.length) {
      endpointsTableContainer.innerHTML = '<p style="text-align: center; color: var(--text-secondary); padding: 20px;">No provider endpoints found.</p>';
      return;
    }

    const searchQuery = modalProviderFilter ? modalProviderFilter.value.trim().toLowerCase() : "";

    let filtered = rawLoadedEndpoints.filter((e) => {
      const name = (e.provider_name || e.name || "").toLowerCase();
      if (searchQuery && !name.includes(searchQuery)) return false;

      // Threshold: Min Uptime %
      if (thresholdFilters.minUptime !== null && !isNaN(thresholdFilters.minUptime)) {
        const up = getUptimeVal(e);
        if (up === null || up < thresholdFilters.minUptime) return false;
      }

      // Threshold: Max Prompt Price $/1M
      if (thresholdFilters.maxPromptPrice !== null && !isNaN(thresholdFilters.maxPromptPrice)) {
        const p1m = e.pricing?.prompt !== undefined && e.pricing?.prompt !== null ? parseFloat(e.pricing.prompt) * 1e6 : 0;
        if (p1m > thresholdFilters.maxPromptPrice) return false;
      }

      // Threshold: Max Completion Price $/1M
      if (thresholdFilters.maxCompletionPrice !== null && !isNaN(thresholdFilters.maxCompletionPrice)) {
        const c1m = e.pricing?.completion !== undefined && e.pricing?.completion !== null ? parseFloat(e.pricing.completion) * 1e6 : 0;
        if (c1m > thresholdFilters.maxCompletionPrice) return false;
      }

      return true;
    });

    filtered.sort((a, b) => {
      for (const sortItem of sortStack) {
        const { col, dir } = sortItem;
        let valA, valB;
        let isNumeric = false;

        if (col === "provider") {
          valA = (a.provider_name || a.name || "").toLowerCase();
          valB = (b.provider_name || b.name || "").toLowerCase();
        } else if (col === "price_prompt") {
          valA = a.pricing?.prompt !== undefined && a.pricing?.prompt !== null ? parseFloat(a.pricing.prompt) : Infinity;
          valB = b.pricing?.prompt !== undefined && b.pricing?.prompt !== null ? parseFloat(b.pricing.prompt) : Infinity;
          isNumeric = true;
        } else if (col === "price_completion") {
          valA = a.pricing?.completion !== undefined && a.pricing?.completion !== null ? parseFloat(a.pricing.completion) : Infinity;
          valB = b.pricing?.completion !== undefined && b.pricing?.completion !== null ? parseFloat(b.pricing.completion) : Infinity;
          isNumeric = true;
        } else if (col === "quantization") {
          valA = (a.quantization || "native").toLowerCase();
          valB = (b.quantization || "native").toLowerCase();
        } else if (col === "uptime") {
          valA = getUptimeVal(a);
          valB = getUptimeVal(b);
          if (valA === null) valA = dir === "asc" ? Infinity : -Infinity;
          if (valB === null) valB = dir === "asc" ? Infinity : -Infinity;
          isNumeric = true;
        }

        if (typeof valA === "number" && isNaN(valA)) valA = dir === "asc" ? Infinity : -Infinity;
        if (typeof valB === "number" && isNaN(valB)) valB = dir === "asc" ? Infinity : -Infinity;

        let res = 0;
        if (isNumeric) {
          res = valA - valB;
        } else {
          res = valA.localeCompare(valB);
        }

        if (res !== 0) {
          return dir === "asc" ? res : -res;
        }
      }
      return 0;
    });

    const renderHeaderCell = (colKey, title) => {
      const stackIndex = sortStack.findIndex((s) => s.col === colKey);
      const isActive = stackIndex !== -1;
      let badgeHTML = "";
      let iconHTML = "";
      let cls = "sortable-header";

      if (isActive) {
        cls += " active-sort";
        const dir = sortStack[stackIndex].dir;
        badgeHTML = `<span class="sort-rank-badge">${stackIndex + 1}</span>`;
        iconHTML = `<span class="sort-icon">${dir === "asc" ? "▲" : "▼"}</span>`;
      } else {
        iconHTML = `<span class="sort-icon"></span>`;
      }

      return `<th class="${cls}" data-sort-col="${colKey}">${badgeHTML}${title}${iconHTML}</th>`;
    };

    let html = '<table style="width: 100%; border-collapse: collapse; text-align: left;">';
    html += '<thead style="border-bottom: 1px solid rgba(255,255,255,0.15); font-size: 0.8rem; color: var(--text-secondary);">';
    html += '<tr>';
    html += renderHeaderCell("provider", "Provider");
    html += renderHeaderCell("price_prompt", "Prompt (1M)");
    html += renderHeaderCell("price_completion", "Completion (1M)");
    html += renderHeaderCell("quantization", "Quantization");
    html += renderHeaderCell("uptime", "Uptime");
    html += '<th style="padding: 8px; text-align: center;" class="non-sortable">Action</th>';
    html += '</tr></thead><tbody>';

    if (!filtered.length) {
      html += '<tr><td colspan="6" style="text-align: center; padding: 20px; color: var(--text-secondary);">No providers match current threshold filters.</td></tr>';
    } else {
      filtered.forEach((e) => {
        const name = e.provider_name || e.name || "Unknown";
        const pPrice = e.pricing?.prompt !== undefined && e.pricing?.prompt !== null ? "$" + (parseFloat(e.pricing.prompt) * 1e6).toFixed(3) : "N/A";
        const cPrice = e.pricing?.completion !== undefined && e.pricing?.completion !== null ? "$" + (parseFloat(e.pricing.completion) * 1e6).toFixed(3) : "N/A";
        const quant = e.quantization || "native";

        const upVal = getUptimeVal(e);
        const uptimeStr = upVal !== null ? upVal.toFixed(1) + "%" : "N/A";

        const isOnline = (e.status === 0 || e.status === undefined) && (upVal === null || upVal > 50);
        const isSelected = selectedProvider && selectedProvider.toLowerCase() === name.toLowerCase();

        html += `<tr class="provider-row" data-provider="${escapeHtml(name)}" style="border-bottom: 1px solid rgba(255,255,255,0.05); opacity: ${isOnline ? "1" : "0.5"}; cursor: pointer; background: ${isSelected ? "rgba(76, 217, 100, 0.12)" : "transparent"};">`;
        html += `<td style="padding: 8px; font-weight: 600;">${escapeHtml(name)} ${isOnline ? "🟢" : "🔴"}</td>`;
        html += `<td style="padding: 8px;">${pPrice}</td>`;
        html += `<td style="padding: 8px;">${cPrice}</td>`;
        html += `<td style="padding: 8px; font-family: monospace;">${escapeHtml(quant)}</td>`;
        html += `<td style="padding: 8px;">${uptimeStr}</td>`;
        html += `<td style="padding: 8px; text-align: center;">`;
        if (isSelected) {
          html += `<span style="color: #4cd964; font-weight: 600; font-size: 0.8rem;">✓ Pinned</span>`;
        } else {
          html += `<button type="button" class="btn-select-provider button-dark-utility" data-provider="${escapeHtml(name)}" style="font-size: 0.72rem; padding: 2px 8px;">Select</button>`;
        }
        html += `</td></tr>`;
      });
    }

    html += '</tbody></table>';
    endpointsTableContainer.innerHTML = html;

    // Table header click listeners
    endpointsTableContainer.querySelectorAll("th.sortable-header").forEach((th) => {
      th.addEventListener("click", () => {
        const col = th.getAttribute("data-sort-col");
        if (!col) return;

        const existingIndex = sortStack.findIndex((s) => s.col === col);
        if (existingIndex !== -1) {
          sortStack[existingIndex].dir = sortStack[existingIndex].dir === "asc" ? "desc" : "asc";
        } else {
          const defaultDir = (col === "uptime" || col === "tps") ? "desc" : "asc";
          sortStack.push({ col: col, dir: defaultDir });
        }
        syncSortDropdownSelect();
        renderEndpointsTable();
      });
    });

    // Row selection click listeners
    endpointsTableContainer.querySelectorAll(".provider-row").forEach((row) => {
      row.addEventListener("click", () => {
        const pName = row.getAttribute("data-provider");
        if (pName) {
          selectedProvider = pName;
          updateProviderBadge();
          updateModelPricingDisplay(modelSearch ? modelSearch.value.trim() : "");
          renderEndpointsTable();
          showToast(`Pinned provider routing to ${pName}`, "success");
        }
      });
    });
  }

  if (modalProviderFilter) {
    modalProviderFilter.addEventListener("input", renderEndpointsTable);
  }
  if (modalSortSelect) {
    modalSortSelect.addEventListener("change", () => {
      const val = modalSortSelect.value;
      if (val === "price_asc") { sortStack = [{ col: "price_prompt", dir: "asc" }]; }
      else if (val === "price_desc") { sortStack = [{ col: "price_prompt", dir: "desc" }]; }
      else if (val === "completion_asc") { sortStack = [{ col: "price_completion", dir: "asc" }]; }
      else if (val === "completion_desc") { sortStack = [{ col: "price_completion", dir: "desc" }]; }
      else if (val === "tps_desc") { sortStack = [{ col: "tps", dir: "desc" }]; }
      else if (val === "uptime_desc") { sortStack = [{ col: "uptime", dir: "desc" }]; }
      else if (val === "provider_asc") { sortStack = [{ col: "provider", dir: "asc" }]; }
      else if (val === "provider_desc") { sortStack = [{ col: "provider", dir: "desc" }]; }
      else if (val === "quant_asc") { sortStack = [{ col: "quantization", dir: "asc" }]; }
      renderEndpointsTable();
    });
  }

  if (btnViewEndpoints) {
    btnViewEndpoints.addEventListener("click", async (ev) => {
      if (ev) {
        ev.preventDefault();
        ev.stopPropagation();
      }

      let selectedModel = (modelSearch ? modelSearch.value : "").trim();
      if (selectedModel.includes(" ")) {
        selectedModel = selectedModel.split(" ")[0];
      }

      if (!selectedModel) {
        showToast("Please select an LLM model first to view its providers.", "error");
        return;
      }

      if (endpointsModalBackdrop) {
        endpointsModalBackdrop.classList.remove("hidden");
      }

      updateProviderBadge();

      if (endpointsTableContainer) {
        endpointsTableContainer.innerHTML = '<p style="text-align: center; color: var(--text-secondary); padding: 20px;">Fetching live providers for <strong>' + escapeHtml(selectedModel) + '</strong>...</p>';
      }

      try {
        const res = await apiFetch("/api/model-endpoints?model_id=" + encodeURIComponent(selectedModel));
        const data = await res.json();
        rawLoadedEndpoints = data?.data?.endpoints || [];
        renderEndpointsTable();
      } catch (err) {
        if (endpointsTableContainer) {
          endpointsTableContainer.innerHTML = '<p style="text-align: center; color: #ff5555; padding: 20px;">Failed to load endpoints: ' + escapeHtml(err.message) + '</p>';
        }
      }
    });
  }

  if (btnCloseEndpointsModal) {
    btnCloseEndpointsModal.addEventListener("click", () => {
      if (endpointsModalBackdrop) {
        endpointsModalBackdrop.classList.add("hidden");
      }
    });
  }

  if (endpointsModalBackdrop) {
    endpointsModalBackdrop.addEventListener("click", (e) => {
      if (e.target === endpointsModalBackdrop) {
        endpointsModalBackdrop.classList.add("hidden");
      }
    });
  }
});



