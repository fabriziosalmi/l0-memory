const ENDPOINT = 'http://127.0.0.1:8080';

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.create({
    id: "save-to-l0-memory",
    title: "Save selection to l0-memory",
    contexts: ["selection"]
  });
});

// Flash the toolbar badge so a background (context-menu) save gives visible
// feedback — the popup isn't open, so console logs alone are invisible.
function flashBadge(text, color) {
  chrome.action.setBadgeBackgroundColor({ color });
  chrome.action.setBadgeText({ text });
  setTimeout(() => chrome.action.setBadgeText({ text: "" }), 4000);
}

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId === "save-to-l0-memory" && info.selectionText && tab) {
    // Generate a unique key using tab title and last 6 digits of timestamp
    let titleSnippet = "clip";
    if (tab.title) {
      titleSnippet = tab.title
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/(^-|-$)/g, '')
        .substring(0, 20);
    }
    const uniqueKey = `${titleSnippet || "clip"}-${Date.now().toString().slice(-6)}`;

    // Token + default scope come from the popup's saved settings.
    const { token, scope } = await chrome.storage.local.get(['token', 'scope']);
    if (!token) {
      console.error('l0-memory clipper: no server token set — open the popup and paste the token from `ltm serve`.');
      flashBadge("!", "#c0392b");
      return;
    }

    const payload = {
      scope: scope || "web",
      key: uniqueKey,
      value: info.selectionText,
      tags: "web-clip",
      origin: tab.url,
      origin_agent: "web-clipper"
    };

    try {
      const response = await fetch(`${ENDPOINT}/memories`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-LTM-Token': token
        },
        body: JSON.stringify(payload)
      });

      if (response.status === 401) {
        console.error('l0-memory clipper: invalid token — re-copy it from `ltm serve` into the popup.');
        flashBadge("!", "#c0392b");
      } else if (!response.ok) {
        const errorText = await response.text();
        console.error(`l0-memory clipper: save failed, server returned ${response.status}: ${errorText}`);
        flashBadge("!", "#c0392b");
      } else {
        console.log(`l0-memory clipper: saved selection under key "${uniqueKey}"`);
        flashBadge("✓", "#27ae60");
      }
    } catch (err) {
      console.error(`l0-memory clipper: cannot reach ${ENDPOINT} — is \`ltm serve\` running?`, err);
      flashBadge("!", "#c0392b");
    }
  }
});
