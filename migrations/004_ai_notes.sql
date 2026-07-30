-- AI 结论台账（产品稿 Q8=C：做存档与台账，不做决策联动）
--
-- 为什么要存：不存的话这条链路是单向的 —— 数据出去了，AI 的判断留在浏览器
-- 另一个标签页里，三个月后想不起来「当初为什么加仓医药」。
-- 存下来之后，基金详情里能显示「07-22 说等回踩 20 日线；现在偏离 +1.2%」，
-- 让旧结论变成可验证的东西。
--
-- 只写本地 sanhu.db，不上传任何地方。

CREATE TABLE IF NOT EXISTS ai_notes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    scene       TEXT NOT NULL,              -- 场景 id，对应 aiprompt.Scenes
    scene_title TEXT NOT NULL DEFAULT '',   -- 存生成时的标题，改文案不影响历史记录
    codes       TEXT NOT NULL DEFAULT '',   -- 涉及的基金代码，逗号分隔（便于 LIKE 查）
    summary     TEXT NOT NULL DEFAULT '',   -- 用户写的一句话小结，台账列表显示这个
    content     TEXT NOT NULL,              -- AI 回答全文，工具只存不解析
    created_at  TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

CREATE INDEX IF NOT EXISTS idx_ai_notes_created ON ai_notes(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ai_notes_scene   ON ai_notes(scene);
