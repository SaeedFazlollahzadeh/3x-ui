(function () {
  // Vue app for Subscription page
  const el = document.getElementById('subscription-data');
  if (!el) return;
  const textarea = document.getElementById('subscription-links');
  const rawLinks = (textarea?.value || '').split('\n').filter(Boolean);

  const data = {
    sId: el.getAttribute('data-sid') || '',
    enabled: (el.getAttribute('data-enabled') || '').toLowerCase() === 'true',
    subUrl: el.getAttribute('data-sub-url') || '',
    subJsonUrl: el.getAttribute('data-subjson-url') || '',
    subClashUrl: el.getAttribute('data-subclash-url') || '',
    download: el.getAttribute('data-download') || '',
    upload: el.getAttribute('data-upload') || '',
    used: el.getAttribute('data-used') || '',
    total: el.getAttribute('data-total') || '',
    remained: el.getAttribute('data-remained') || '',
    expireMs: (parseInt(el.getAttribute('data-expire') || '0', 10) || 0) * 1000,
    lastOnlineMs: (parseInt(el.getAttribute('data-lastonline') || '0', 10) || 0),
    downloadByte: parseInt(el.getAttribute('data-downloadbyte') || '0', 10) || 0,
    uploadByte: parseInt(el.getAttribute('data-uploadbyte') || '0', 10) || 0,
    totalByte: parseInt(el.getAttribute('data-totalbyte') || '0', 10) || 0,
    datepicker: el.getAttribute('data-datepicker') || 'gregorian',
  };
  const today = new Date().toISOString().slice(0, 10);
  const weekAgo = new Date(Date.now() - (6 * 24 * 60 * 60 * 1000)).toISOString().slice(0, 10);

  // Normalize lastOnline to milliseconds if it looks like seconds
  if (data.lastOnlineMs && data.lastOnlineMs < 10_000_000_000) {
    data.lastOnlineMs *= 1000;
  }

  function renderLink(item) {
    return (
      Vue.h('a-list-item', {}, [
        Vue.h('a-space', { props: { size: 'small' } }, [
          Vue.h('a-button', { props: { size: 'small' }, on: { click: () => copy(item) } }, [Vue.h('a-icon', { props: { type: 'copy' } })]),
          Vue.h('span', { class: 'break-all' }, item)
        ])
      ])
    );
  }

  function copy(text) {
    ClipboardManager.copyText(text).then(ok => {
      const messageType = ok ? 'success' : 'error';
      Vue.prototype.$message[messageType](ok ? 'Copied' : 'Copy failed');
    });
  }

  function open(url) {
    window.location.href = url;
  }

  function drawQR(value) {
    try {
      new QRious({ element: document.getElementById('qrcode'), value, size: 220 });
    } catch (e) {
      console.warn(e);
    }
  }

  // Try to extract a human label (email/ps) from different link types
  function linkName(link, idx) {
    try {
      if (link.startsWith('vmess://')) {
        const json = JSON.parse(atob(link.replace('vmess://', '')));
        if (json.ps) return json.ps;
        if (json.add && json.id) return json.add; // fallback host
      } else if (link.startsWith('vless://') || link.startsWith('trojan://')) {
        const hashIdx = link.indexOf('#');
        if (hashIdx !== -1) return decodeURIComponent(link.substring(hashIdx + 1));
        const qIdx = link.indexOf('?');
        if (qIdx !== -1) {
          const qs = new URL('http://x/?' + link.substring(qIdx + 1, hashIdx !== -1 ? hashIdx : undefined)).searchParams;
          if (qs.get('remark')) return qs.get('remark');
          if (qs.get('email')) return qs.get('email');
        }
        const at = link.indexOf('@');
        const protSep = link.indexOf('://');
        if (at !== -1 && protSep !== -1) return link.substring(protSep + 3, at);
      } else if (link.startsWith('ss://')) {
        const hashIdx = link.indexOf('#');
        if (hashIdx !== -1) return decodeURIComponent(link.substring(hashIdx + 1));
      }
    } catch (e) { /* ignore and fallback */ }
    return 'Link ' + (idx + 1);
  }

  const app = new Vue({
    delimiters: ['[[', ']]'],
    el: '#app',
    data: {
      themeSwitcher,
      app: data,
      links: rawLinks,
      usageFrom: weekAgo,
      usageTo: today,
      dailyUsage: {
        loading: false,
        from: weekAgo,
        to: today,
        clientRows: [],
        points: [],
        up: 0,
        down: 0,
        total: 0,
      },
      lang: '',
      viewportWidth: (typeof window !== 'undefined' ? window.innerWidth : 1024),
    },
    async mounted() {
      this.lang = LanguageManager.getLanguage();
      const tpl = document.getElementById('subscription-data');
      const sj = tpl ? tpl.getAttribute('data-subjson-url') : '';
      const sc = tpl ? tpl.getAttribute('data-subclash-url') : '';
      if (sj) this.app.subJsonUrl = sj;
      if (sc) this.app.subClashUrl = sc;
      drawQR(this.app.subUrl);
      try {
        const elJson = document.getElementById('qrcode-subjson');
        if (elJson && this.app.subJsonUrl) {
          new QRious({ element: elJson, value: this.app.subJsonUrl, size: 220 });
        }
        const elClash = document.getElementById('qrcode-subclash');
        if (elClash && this.app.subClashUrl) {
          new QRious({ element: elClash, value: this.app.subClashUrl, size: 220 });
        }
      } catch (e) { /* ignore */ }
      this._onResize = () => { this.viewportWidth = window.innerWidth; };
      window.addEventListener('resize', this._onResize);
      this.loadDailyUsage();
    },
    beforeDestroy() {
      if (this._onResize) window.removeEventListener('resize', this._onResize);
    },
    computed: {
      isMobile() {
        return this.viewportWidth < 576;
      },
      isUnlimited() {
        return !this.app.totalByte;
      },
      isActive() {
        const now = Date.now();
        const enabledOk = this.app.enabled;
        const expiryOk = !this.app.expireMs || this.app.expireMs >= now;
        const trafficOk = !this.app.totalByte || (this.app.uploadByte + this.app.downloadByte) <= this.app.totalByte;
        return enabledOk && expiryOk && trafficOk;
      },
      shadowrocketUrl() {
        const separator = this.app.subUrl.includes('?') ? '&' : '?';
        const rawUrl = this.app.subUrl + separator + 'flag=shadowrocket';
        const base64Url = encodeURIComponent(btoa(rawUrl));
        const remark = encodeURIComponent(this.app.sId || 'Subscription');
        return `shadowrocket://add/sub/${base64Url}?remark=${remark}`;
      },
      v2boxUrl() {
        return `v2box://install-sub?url=${encodeURIComponent(this.app.subUrl)}&name=${encodeURIComponent(this.app.sId)}`;
      },
      streisandUrl() {
        return `streisand://import/${encodeURIComponent(this.app.subUrl)}`;
      },
      v2raytunUrl() {
        return this.app.subUrl;
      },
      npvtunUrl() {
        return this.app.subUrl;
      },
      happUrl() {
        return `happ://add/${this.app.subUrl}`;
      },
      dailyBarMax() {
        return this.dailyUsage.points.reduce((max, point) => Math.max(max, point.up || 0, point.down || 0, point.total || 0), 0) || 1;
      },
      dailyChartBars() {
        const points = this.dailyUsage.points || [];
        const spacing = points.length > 1 ? Math.max(60, Math.floor(760 / points.length)) : 60;
        return points.map((point, index) => {
          const scale = 150 / this.dailyBarMax;
          return {
            key: `${point.date}-${index}`,
            x: 110 + (index * spacing),
            upHeight: Math.max((point.up || 0) * scale, point.up ? 2 : 0),
            downHeight: Math.max((point.down || 0) * scale, point.down ? 2 : 0),
            totalHeight: Math.max((point.total || 0) * scale, point.total ? 2 : 0),
            shortLabel: point.date ? point.date.slice(5) : `#${index + 1}`,
          };
        });
      }
    },
    methods: {
      renderLink,
      copy,
      open,
      linkName,
      i18nLabel(key) {
        return '{{ i18n "' + key + '" }}';
      },
      async loadDailyUsage() {
        this.dailyUsage.loading = true;
        try {
          const usageUrl = window.location.pathname.replace(/\/$/, '') + '/usage';
          const url = new URL(usageUrl, window.location.origin);
          url.searchParams.set('from', this.usageFrom);
          url.searchParams.set('to', this.usageTo);
          const resp = await fetch(url.toString(), {
            method: 'GET',
            headers: {
              'Accept': 'application/json',
            },
          });
          const payload = await resp.json();
          if (!payload || !payload.success || !payload.obj) return;
          this.dailyUsage = {
            loading: false,
            from: payload.obj.from || this.usageFrom,
            to: payload.obj.to || this.usageTo,
            clientRows: Array.isArray(payload.obj.clientRows) ? payload.obj.clientRows : [],
            points: Array.isArray(payload.obj.points) ? payload.obj.points : [],
            up: payload.obj.up || 0,
            down: payload.obj.down || 0,
            total: payload.obj.total || 0,
          };
          this.usageFrom = this.dailyUsage.from;
          this.usageTo = this.dailyUsage.to;
        } catch (error) {
          console.error('Failed to load sub daily usage:', error);
          Vue.prototype.$message.error('Failed to load usage');
        } finally {
          this.dailyUsage.loading = false;
        }
      },
    },
  });
})();
