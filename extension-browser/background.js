chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.create({
    id: "save-to-l0-memory",
    title: "Save selection to l0-memory",
    contexts: ["selection"]
  });
});

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

    const payload = {
      scope: "user",
      key: uniqueKey,
      value: info.selectionText,
      tags: "web-clip",
      origin: tab.url,
      origin_agent: "web-clipper"
    };

    try {
      const response = await fetch('http://127.0.0.1:8080/memories', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify(payload)
      });

      if (!response.ok) {
        const errorText = await response.text();
        console.error(`l0-memory clipper: Failed to save. Server returned ${response.status}: ${errorText}`);
      } else {
        console.log(`l0-memory clipper: Successfully saved selected text under key "${uniqueKey}"`);
      }
    } catch (err) {
      console.error('l0-memory clipper: Error sending request to local REST server:', err);
    }
  }
});
