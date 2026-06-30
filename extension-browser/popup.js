document.addEventListener('DOMContentLoaded', async () => {
  const keyInput = document.getElementById('key');
  const valueInput = document.getElementById('value');
  const scopeInput = document.getElementById('scope');
  const tagsInput = document.getElementById('tags');
  const originInput = document.getElementById('origin');
  const saveBtn = document.getElementById('save-btn');
  const statusDiv = document.getElementById('status');

  // Helper to show status message
  const showStatus = (msg, isError = false) => {
    statusDiv.textContent = msg;
    statusDiv.className = `status-msg ${isError ? 'error' : 'success'}`;
    setTimeout(() => {
      statusDiv.textContent = '';
      statusDiv.className = 'status-msg';
    }, 4000);
  };

  // Get active tab details to pre-populate origin
  try {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (tab) {
      originInput.value = tab.url;
      
      // Auto-suggest a key based on the page title
      if (tab.title) {
        const cleanKey = tab.title
          .toLowerCase()
          .replace(/[^a-z0-9]+/g, '-')
          .replace(/(^-|-$)/g, '')
          .substring(0, 30);
        keyInput.value = cleanKey;
      }

      // Try to get selected text from the page
      try {
        const results = await chrome.scripting.executeScript({
          target: { tabId: tab.id },
          func: () => window.getSelection().toString()
        });
        if (results && results[0] && results[0].result) {
          valueInput.value = results[0].result;
        }
      } catch (err) {
        // Silently fail if scripting is not allowed on this page (e.g. chrome:// URLs)
      }
    }
  } catch (err) {
    console.error('Failed to get active tab info:', err);
  }

  // Handle Save button click
  saveBtn.addEventListener('click', async () => {
    const key = keyInput.value.trim();
    const value = valueInput.value.trim();
    const scope = scopeInput.value.trim() || 'user';
    const tags = tagsInput.value.trim();
    const origin = originInput.value.trim();

    if (!key || !value) {
      showStatus('Key and Content are required!', true);
      return;
    }

    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving...';

    try {
      const response = await fetch('http://127.0.0.1:8080/memories', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          scope,
          key,
          value,
          tags,
          origin,
          origin_agent: 'web-clipper'
        })
      });

      if (!response.ok) {
        const errorData = await response.json();
        throw new Error(errorData.error || `HTTP ${response.status}`);
      }

      showStatus('Memory saved successfully!');
      valueInput.value = ''; // clear value input after successful save
    } catch (err) {
      showStatus(`Error: ${err.message}`, true);
    } finally {
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save to Memory';
    }
  });
});
