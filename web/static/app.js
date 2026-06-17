// SysProbe Dashboard - Main Application
(function() {
  'use strict';

  const REFRESH_INTERVAL = 10000; // 10s
  let chartInstance = null;
  let selectedServer = null;

  // --- Initialization ---
  document.addEventListener('DOMContentLoaded', () => {
    updateClock();
    setInterval(updateClock, 1000);
    fetchServers();
    setInterval(fetchServers, REFRESH_INTERVAL);
    fetchAlerts();
    setInterval(fetchAlerts, 30000);

    // Chart controls
    document.getElementById('chart-close').addEventListener('click', closeChart);
    document.getElementById('chart-metric').addEventListener('change', refreshChart);
    document.getElementById('chart-hours').addEventListener('change', refreshChart);
  });

  // --- Clock ---
  function updateClock() {
    const now = new Date();
    document.getElementById('clock').textContent = now.toLocaleString('zh-CN');
  }

  // --- Fetch Servers ---
  async function fetchServers() {
    try {
      const resp = await fetch('/api/servers');
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      const servers = await resp.json();
      renderServers(servers);
    } catch (err) {
      console.error('fetchServers:', err);
    }
  }

  function renderServers(servers) {
    const tbody = document.getElementById('server-tbody');
    const online = servers.filter(s => s.status === 'online').length;
    const offline = servers.length - online;

    document.getElementById('count-online').textContent = online;
    document.getElementById('count-offline').textContent = offline;
    document.getElementById('count-total').textContent = servers.length;

    if (servers.length === 0) {
      tbody.innerHTML = '<tr class="empty-row"><td colspan="10">暂无服务器数据</td></tr>';
      return;
    }

    tbody.innerHTML = servers.map(s => {
      const statusClass = s.status === 'online' ? 'online' : 'offline';
      const cpuBarClass = barClass(s.cpu_percent);
      const memBarClass = barClass(s.mem_percent);
      const diskBarClass = barClass(s.disk_percent);
      const rxRate = formatRate(s.net_rx_rate);
      const txRate = formatRate(s.net_tx_rate);
      const uptime = formatUptime(s.uptime);
      const lastSeen = formatTimeAgo(s.last_seen);
      const selected = selectedServer === s.name ? ' selected' : '';

      return `<tr class="server-row${selected}" data-name="${escHtml(s.name)}">
        <td><span class="status-dot ${statusClass}" title="${s.status}"></span></td>
        <td><strong>${escHtml(s.name)}</strong><br><small>${escHtml(s.os_name || '')}</small></td>
        <td><div class="bar-wrap"><div class="bar-fill ${cpuBarClass}" style="width:${Math.min(s.cpu_percent, 100)}%"></div></div><span class="bar-value">${s.cpu_percent.toFixed(1)}%</span></td>
        <td><div class="bar-wrap"><div class="bar-fill ${memBarClass}" style="width:${Math.min(s.mem_percent, 100)}%"></div></div><span class="bar-value">${s.mem_percent.toFixed(1)}%</span></td>
        <td><div class="bar-wrap"><div class="bar-fill ${diskBarClass}" style="width:${Math.min(s.disk_percent, 100)}%"></div></div><span class="bar-value">${s.disk_percent.toFixed(1)}%</span></td>
        <td class="net-rate"><span class="rx">↓ ${rxRate}</span></td>
        <td class="net-rate"><span class="tx">↑ ${txRate}</span></td>
        <td>${s.load_1.toFixed(1)} / ${s.load_5.toFixed(1)} / ${s.load_15.toFixed(1)}</td>
        <td>${uptime}</td>
        <td>${lastSeen}</td>
      </tr>`;
    }).join('');

    // Click handlers
    tbody.querySelectorAll('.server-row').forEach(row => {
      row.addEventListener('click', () => {
        const name = row.dataset.name;
        if (selectedServer === name) {
          closeChart();
        } else {
          selectServer(name);
        }
      });
    });
  }

  function barClass(val) {
    if (val > 90) return 'high';
    if (val > 70) return 'mid';
    return 'low';
  }

  // --- Chart ---
  function selectServer(name) {
    selectedServer = name;
    document.getElementById('chart-title').textContent = '历史趋势 - ' + name;
    document.getElementById('chart-panel').style.display = 'block';
    // Re-render to update selection
    document.querySelectorAll('.server-row').forEach(r => {
      r.classList.toggle('selected', r.dataset.name === name);
    });
    refreshChart();
  }

  function closeChart() {
    selectedServer = null;
    document.getElementById('chart-panel').style.display = 'none';
    if (chartInstance) {
      chartInstance.destroy();
      chartInstance = null;
    }
    document.querySelectorAll('.server-row').forEach(r => r.classList.remove('selected'));
  }

  async function refreshChart() {
    if (!selectedServer) return;

    const metric = document.getElementById('chart-metric').value;
    const hours = document.getElementById('chart-hours').value;

    try {
      const resp = await fetch('/api/history/' + encodeURIComponent(selectedServer) + '?hours=' + hours);
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      const data = await resp.json();

      const labels = data.map(d => {
        const t = new Date(d.timestamp);
        return t.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
      });
      const values = data.map(d => d[metric] || 0);

      const metricLabels = {
        cpu_percent: 'CPU %',
        mem_percent: '内存 %',
        disk_percent: '磁盘 %',
        net_rx_rate: '入站速率 (bytes/s)',
        net_tx_rate: '出站速率 (bytes/s)',
        load_1: 'Load 1min',
      };

      const ctx = document.getElementById('history-chart').getContext('2d');

      if (chartInstance) chartInstance.destroy();

      chartInstance = new Chart(ctx, {
        type: 'line',
        data: {
          labels: labels,
          datasets: [{
            label: metricLabels[metric] || metric,
            data: values,
            borderColor: '#58a6ff',
            backgroundColor: 'rgba(88, 166, 255, 0.1)',
            fill: true,
            tension: 0.3,
            pointRadius: 0,
            borderWidth: 2,
          }]
        },
        options: {
          responsive: true,
          maintainAspectRatio: false,
          animation: { duration: 300 },
          scales: {
            x: {
              ticks: { color: '#6e7681', maxTicksLimit: 20, maxRotation: 0 },
              grid: { color: '#1e2130' }
            },
            y: {
              ticks: { color: '#6e7681' },
              grid: { color: '#1e2130' },
              beginAtZero: true,
            }
          },
          plugins: {
            legend: { display: false },
          }
        }
      });
    } catch (err) {
      console.error('fetchHistory:', err);
    }
  }

  // --- Alerts ---
  async function fetchAlerts() {
    try {
      const resp = await fetch('/api/alerts');
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      const alerts = await resp.json();
      renderAlerts(alerts);
    } catch (err) {
      console.error('fetchAlerts:', err);
    }
  }

  function renderAlerts(alerts) {
    const container = document.getElementById('alert-list');
    // Count alerts from last 24h
    const dayAgo = Date.now() - 86400000;
    const recentAlerts = alerts.filter(a => {
      const t = new Date(a.sent_at + 'Z').getTime();
      return t > dayAgo;
    });
    document.getElementById('count-alerts').textContent = recentAlerts.length;

    if (alerts.length === 0) {
      container.innerHTML = '<div class="alert-item" style="color:var(--text-dim)">暂无告警</div>';
      return;
    }

    container.innerHTML = alerts.slice(0, 20).map(a => {
      const time = formatTimeAgo(a.sent_at);
      const typeClass = a.type === 'traffic_rx' || a.type === 'traffic_tx' ? 'traffic_rx' : a.type;
      return `<div class="alert-item">
        <span class="alert-type ${typeClass}">${a.type}</span>
        <span>${escHtml(a.message)}</span>
        <span class="alert-time">${time}</span>
      </div>`;
    }).join('');
  }

  // --- Formatters ---
  function formatRate(bytesPerSec) {
    if (!bytesPerSec || bytesPerSec < 0) return '0 B/s';
    const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
    let v = bytesPerSec;
    let i = 0;
    while (v >= 1000 && i < units.length - 1) {
      v /= 1000;
      i++;
    }
    return v.toFixed(1) + ' ' + units[i];
  }

  function formatUptime(seconds) {
    if (!seconds || seconds < 0) return '--';
    const d = Math.floor(seconds / 86400);
    const h = Math.floor((seconds % 86400) / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    if (d > 0) return d + 'd ' + h + 'h';
    if (h > 0) return h + 'h ' + m + 'm';
    return m + 'm';
  }

  function formatTimeAgo(ts) {
    if (!ts) return '--';
    const t = new Date(ts + 'Z').getTime();
    const sec = Math.floor((Date.now() - t) / 1000);
    if (sec < 5) return '刚刚';
    if (sec < 60) return sec + '秒前';
    if (sec < 3600) return Math.floor(sec / 60) + '分钟前';
    if (sec < 86400) return Math.floor(sec / 3600) + '小时前';
    return Math.floor(sec / 86400) + '天前';
  }

  function escHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
  }
})();