/* 术语 Tooltip 组件 — tooltip.js
 * 依赖：terms.js（必须先加载）
 * 用法：给任意元素加 data-term="净值" 属性，自动挂载 ⓘ 图标和弹出气泡
 * 动态节点支持：MutationObserver 监听 DOM 变化，动态插入的节点也会被挂载
 */
(function () {
  'use strict';

  // ── 定位气泡 ──────────────────────────────────────────────────────────────
  function positionBubble(bubble, anchor) {
    const rect = anchor.getBoundingClientRect();
    const bubbleW = 260;
    const gap = 8;

    let left = rect.right + gap;
    let top = rect.top;

    // 超出右边界则改到左侧
    if (left + bubbleW > window.innerWidth - 8) {
      left = rect.left - bubbleW - gap;
    }
    // 超出底部则上移
    const bubbleH = 120; // 估算高度
    if (top + bubbleH > window.innerHeight - 8) {
      top = window.innerHeight - bubbleH - 8;
    }
    // 确保不超出左侧
    if (left < 8) left = 8;
    if (top < 8) top = 8;

    bubble.style.left = left + 'px';
    bubble.style.top = top + 'px';
  }

  // ── 关闭气泡 ──────────────────────────────────────────────────────────────
  window.closeTermBubble = function () {
    const existing = document.getElementById('termBubble');
    if (existing) existing.remove();
  };

  // ── 显示气泡 ──────────────────────────────────────────────────────────────
  function showTermBubble(anchor, term, info) {
    window.closeTermBubble();

    const bubble = document.createElement('div');
    bubble.id = 'termBubble';
    bubble.setAttribute('role', 'tooltip');
    bubble.innerHTML =
      '<button class="term-close" onclick="closeTermBubble()" aria-label="关闭">✕</button>' +
      '<strong class="term-name">' + term + '</strong>' +
      '<div class="term-def">' + info.def + '</div>' +
      '<div class="term-analogy">💡 ' + info.analogy + '</div>';

    document.body.appendChild(bubble);
    positionBubble(bubble, anchor);

    // 点页面其他地方关闭
    setTimeout(function () {
      document.addEventListener('click', window.closeTermBubble, { once: true });
    }, 0);
  }

  // ── 挂载单个节点 ─────────────────────────────────────────────────────────
  function mountOne(el) {
    if (el.dataset.tooltipReady) return;
    el.dataset.tooltipReady = '1';

    const term = el.dataset.term;
    if (!window.JIJIN_TERMS) return;
    const info = window.JIJIN_TERMS[term];
    if (!info) return;

    const icon = document.createElement('span');
    icon.className = 'term-icon';
    icon.textContent = ' ⓘ';
    icon.setAttribute('role', 'button');
    icon.setAttribute('tabindex', '0');
    icon.setAttribute('aria-label', '查看"' + term + '"的解释');
    el.appendChild(icon);

    icon.addEventListener('click', function (e) {
      e.stopPropagation();
      showTermBubble(icon, term, info);
    });
    icon.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        showTermBubble(icon, term, info);
      }
    });
  }

  // ── 批量挂载 ─────────────────────────────────────────────────────────────
  window.initTermTooltips = function (root) {
    root = root || document;
    root.querySelectorAll('[data-term]').forEach(mountOne);
  };

  // ── MutationObserver：监听动态节点 ────────────────────────────────────────
  var observer = new MutationObserver(function (mutations) {
    mutations.forEach(function (m) {
      m.addedNodes.forEach(function (node) {
        if (node.nodeType !== 1) return; // 只处理元素节点
        if (node.dataset && node.dataset.term) mountOne(node);
        node.querySelectorAll && node.querySelectorAll('[data-term]').forEach(mountOne);
      });
    });
  });

  // DOM 加载完成后启动
  function bootstrap() {
    window.initTermTooltips(document);
    observer.observe(document.body, { childList: true, subtree: true });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', bootstrap);
  } else {
    bootstrap();
  }
})();
