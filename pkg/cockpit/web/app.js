// shadowtap Cockpit Frontend Application
(function () {
  let isStreamPaused = false;
  let activeFilter = 'all'; // 'all' or 'regressed'
  let searchQuery = '';
  let transactions = [];
  let selectedTxId = null;
  let eventSource = null;

  // DOM Elements
  const globalSRRElem = document.getElementById('global-srr-val');
  const totalShadowedElem = document.getElementById('total-shadowed-val');
  const totalRegressedElem = document.getElementById('total-regressed-val');
  const queueDepthElem = document.getElementById('queue-depth-val');
  const pauseStreamBtn = document.getElementById('pause-stream-btn');
  const routesTbody = document.getElementById('routes-tbody');
  const routesSummary = document.getElementById('routes-summary-text');
  const txListElem = document.getElementById('transactions-list');
  const searchInput = document.getElementById('filter-search');
  const tabAll = document.getElementById('tab-all');
  const tabRegressed = document.getElementById('tab-regressed');

  // Diff Pane Elements
  const diffTitle = document.getElementById('diff-title');
  const diffSubtitle = document.getElementById('diff-subtitle');
  const diffMetaTags = document.getElementById('diff-meta-tags');
  const mismatchesContainer = document.getElementById('mismatches-container');
  const mismatchesCount = document.getElementById('mismatches-count');
  const mismatchesChips = document.getElementById('mismatches-chips');
  const primaryPaneStats = document.getElementById('primary-pane-stats');
  const shadowPaneStats = document.getElementById('shadow-pane-stats');
  const primaryCodeView = document.getElementById('primary-code-view');
  const shadowCodeView = document.getElementById('shadow-code-view');

  // Initialize
  function init() {
    setupEventListeners();
    fetchStats();
    fetchRoutes();
    fetchInitialPairs();
    startEventStream();

    setInterval(fetchStats, 2000);
    setInterval(fetchRoutes, 3000);
  }

  function setupEventListeners() {
    pauseStreamBtn.addEventListener('click', () => {
      isStreamPaused = !isStreamPaused;
      pauseStreamBtn.textContent = isStreamPaused ? 'Resume Stream' : 'Pause Stream';
      pauseStreamBtn.classList.toggle('btn-secondary', !isStreamPaused);
      pauseStreamBtn.style.background = isStreamPaused ? '#f59e0b' : '';
      pauseStreamBtn.style.color = isStreamPaused ? '#000' : '';
    });

    tabAll.addEventListener('click', () => {
      activeFilter = 'all';
      tabAll.classList.add('active');
      tabRegressed.classList.remove('active');
      renderTransactions();
    });

    tabRegressed.addEventListener('click', () => {
      activeFilter = 'regressed';
      tabRegressed.classList.add('active');
      tabAll.classList.remove('active');
      renderTransactions();
    });

    searchInput.addEventListener('input', (e) => {
      searchQuery = e.target.value.toLowerCase().trim();
      renderTransactions();
    });
  }

  // Fetch Summary Stats
  async function fetchStats() {
    try {
      const res = await fetch('/api/stats');
      if (!res.ok) return;
      const data = await res.json();
      globalSRRElem.textContent = (data.global_srr * 100).toFixed(2) + '%';
      totalShadowedElem.textContent = data.total_shadowed.toLocaleString();
      totalRegressedElem.textContent = data.total_regressed.toLocaleString();
      queueDepthElem.textContent = `${data.queue_depth} / 5000`;
    } catch (err) {
      console.warn('Failed to fetch stats:', err);
    }
  }

  // Fetch Route Scorecard
  async function fetchRoutes() {
    try {
      const res = await fetch('/api/routes');
      if (!res.ok) return;
      const routes = await res.json();
      renderRoutes(routes);
    } catch (err) {
      console.warn('Failed to fetch routes:', err);
    }
  }

  function renderRoutes(routes) {
    if (!routes || routes.length === 0) {
      routesTbody.innerHTML = '<tr><td colspan="7" class="empty-state">Awaiting inbound shadowed traffic...</td></tr>';
      routesSummary.textContent = '0 active routes monitored';
      return;
    }

    routesSummary.textContent = `${routes.length} active routes monitored`;
    routesTbody.innerHTML = routes.map(r => {
      const srrPct = (r.srr * 100).toFixed(2);
      const isHealthy = r.srr <= 0.01;
      const deltaMs = r.latency_delta_p99_ms.toFixed(1);
      const deltaSign = r.latency_delta_p99_ms > 0 ? `+${deltaMs}ms` : `${deltaMs}ms`;

      return `
        <tr>
          <td><span class="route-badge">${escapeHtml(r.route)}</span></td>
          <td>${r.total_requests.toLocaleString()}</td>
          <td><strong class="${r.srr > 0 ? 'text-danger' : 'text-success'}">${srrPct}%</strong></td>
          <td>${r.primary_latency_p99_ms.toFixed(1)}ms</td>
          <td>${r.shadow_latency_p99_ms.toFixed(1)}ms</td>
          <td class="${r.latency_delta_p99_ms > 5 ? 'text-warning' : ''}">${deltaSign}</td>
          <td>
            <span class="status-badge ${isHealthy ? 'badge-healthy' : 'badge-danger'}">
              ${isHealthy ? 'HEALTHY' : 'REGRESSED'}
            </span>
          </td>
        </tr>
      `;
    }).join('');
  }

  // Fetch Historical Pairs
  async function fetchInitialPairs() {
    try {
      const res = await fetch('/api/pairs?limit=50');
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        transactions = data;
        renderTransactions();
        if (transactions.length > 0 && !selectedTxId) {
          selectTransaction(transactions[0]);
        }
      }
    } catch (err) {
      console.warn('Failed to fetch pairs:', err);
    }
  }

  // Server-Sent Events Live Stream
  function startEventStream() {
    if (eventSource) {
      eventSource.close();
    }
    eventSource = new EventSource('/api/events/live');

    eventSource.onmessage = (event) => {
      if (isStreamPaused) return;
      try {
        const pair = JSON.parse(event.data);
        transactions.unshift(pair);
        if (transactions.length > 200) {
          transactions.pop();
        }
        renderTransactions();
        if (!selectedTxId) {
          selectTransaction(pair);
        }
      } catch (err) {
        console.error('Error parsing SSE event:', err);
      }
    };

    eventSource.onerror = () => {
      console.warn('SSE stream error, retrying...');
    };
  }

  // Render Transaction List
  function renderTransactions() {
    const filtered = transactions.filter(t => {
      if (activeFilter === 'regressed' && t.diff.is_match) {
        return false;
      }
      if (searchQuery) {
        const matchPath = t.path && t.path.toLowerCase().includes(searchQuery);
        const matchMethod = t.method && t.method.toLowerCase().includes(searchQuery);
        const matchID = t.id && t.id.toLowerCase().includes(searchQuery);
        if (!matchPath && !matchMethod && !matchID) return false;
      }
      return true;
    });

    if (filtered.length === 0) {
      txListElem.innerHTML = '<div class="empty-state">No matching transactions found</div>';
      return;
    }

    txListElem.innerHTML = filtered.map(t => {
      const isRegressed = !t.diff.is_match;
      const isSelected = t.id === selectedTxId;
      const delta = t.diff.latency_delta_ms > 0 ? `+${t.diff.latency_delta_ms.toFixed(1)}ms` : `${t.diff.latency_delta_ms.toFixed(1)}ms`;
      const timeStr = new Date(t.timestamp).toLocaleTimeString();

      return `
        <div class="tx-card ${isSelected ? 'active' : ''}" data-id="${t.id}">
          <div class="tx-top">
            <div class="tx-method-path">
              <span class="method-pill method-${t.method}">${t.method}</span>
              <span class="tx-path" title="${escapeHtml(t.path)}">${escapeHtml(t.path)}</span>
            </div>
            <span class="tx-match-tag ${isRegressed ? 'tag-regress' : 'tag-match'}">
              ${isRegressed ? 'DRIFT' : 'MATCH'}
            </span>
          </div>
          <div class="tx-bottom">
            <span class="tx-time">${timeStr} (${t.id})</span>
            <span class="tx-delta">Δ ${delta}</span>
          </div>
        </div>
      `;
    }).join('');

    // Attach click handlers
    txListElem.querySelectorAll('.tx-card').forEach(card => {
      card.addEventListener('click', () => {
        const id = card.getAttribute('data-id');
        const found = transactions.find(t => t.id === id);
        if (found) {
          selectTransaction(found);
        }
      });
    });
  }

  // Select Transaction & Populate Diff Inspector
  function selectTransaction(t) {
    selectedTxId = t.id;

    // Highlight card
    txListElem.querySelectorAll('.tx-card').forEach(card => {
      card.classList.toggle('active', card.getAttribute('data-id') === t.id);
    });

    diffTitle.textContent = `${t.method} ${t.path}`;
    diffSubtitle.textContent = `Transaction ID: ${t.id} | Timestamp: ${new Date(t.timestamp).toISOString()}`;

    // Status tags
    const isMatch = t.diff.is_match;
    const distance = (t.diff.semantic_distance * 100).toFixed(1);
    diffMetaTags.innerHTML = `
      <span class="status-badge ${isMatch ? 'badge-healthy' : 'badge-danger'}">
        ${isMatch ? 'SEMANTIC MATCH' : 'SEMANTIC REGRESSION'}
      </span>
      <span class="metric-pill" style="padding: 3px 8px; font-size: 0.72rem;">
        DISTANCE: ${distance}%
      </span>
    `;

    // Discrepancies Chips
    const mismatches = t.diff.mismatches || [];
    if (mismatches.length > 0) {
      mismatchesContainer.style.display = 'block';
      mismatchesCount.textContent = mismatches.length;
      mismatchesChips.innerHTML = mismatches.map(m => `
        <div class="chip">
          <span class="chip-kind ${m.kind}">${m.kind}</span>
          <span>${escapeHtml(m.path)}: ${escapeHtml(m.message)}</span>
        </div>
      `).join('');
    } else {
      mismatchesContainer.style.display = 'none';
    }

    // Update Panes
    primaryPaneStats.textContent = `HTTP ${t.primary.status_code} | ${t.primary.latency_ms.toFixed(1)}ms`;
    shadowPaneStats.textContent = `HTTP ${t.shadow.status_code} | ${t.shadow.latency_ms.toFixed(1)}ms`;

    primaryCodeView.textContent = t.diff.primary_clean_json || t.primary.body || '// No payload body';
    shadowCodeView.textContent = t.diff.shadow_clean_json || t.shadow.body || '// No payload body';
  }

  function escapeHtml(str) {
    if (!str) return '';
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  window.addEventListener('DOMContentLoaded', init);
})();
