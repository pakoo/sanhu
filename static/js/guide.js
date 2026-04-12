/* 模块使用说明区组件 — guide.js
 * 依赖：无
 * 用法：
 *   HTML 里加 <div id="guide-{tabName}"> 结构（见 index.html）
 *   switchTab 里调用 initModuleGuide(tabName)
 * localStorage key 格式：guide_closed_{tabName}
 */
(function () {
  'use strict';

  window.initModuleGuide = function (tabName) {
    const guideEl = document.getElementById('guide-' + tabName);
    if (!guideEl) return;

    const body = guideEl.querySelector('.guide-body');
    const collapsed = guideEl.querySelector('.guide-collapsed');
    if (!body || !collapsed) return;

    const closed = localStorage.getItem('guide_closed_' + tabName);
    body.style.display = closed ? 'none' : 'block';
    collapsed.style.display = closed ? 'block' : 'none';
  };

  window.closeGuide = function (tabName) {
    localStorage.setItem('guide_closed_' + tabName, '1');
    const guideEl = document.getElementById('guide-' + tabName);
    if (!guideEl) return;
    const body = guideEl.querySelector('.guide-body');
    const collapsed = guideEl.querySelector('.guide-collapsed');
    if (body) body.style.display = 'none';
    if (collapsed) collapsed.style.display = 'block';
  };

  window.openGuide = function (tabName) {
    localStorage.removeItem('guide_closed_' + tabName);
    const guideEl = document.getElementById('guide-' + tabName);
    if (!guideEl) return;
    const body = guideEl.querySelector('.guide-body');
    const collapsed = guideEl.querySelector('.guide-collapsed');
    if (body) body.style.display = 'block';
    if (collapsed) collapsed.style.display = 'none';
  };
})();
