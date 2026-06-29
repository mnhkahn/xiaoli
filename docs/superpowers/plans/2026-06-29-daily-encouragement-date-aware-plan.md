# Daily Encouragement Date-Aware Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 增强 daily-encouragement 技能，使其能够根据月周期（月初/月中/月末）和星期特性（周一/周三/周五）生成更有针对性的鼓励语，同时使用 `cyeam date holiday` 替代原 curl 调用。

**Architecture:** 纯 skill 文档修改，在 SKILL.md 中更新步骤说明和判断逻辑，skill 的执行引擎会按照新文档的描述来生成鼓励语。

**Tech Stack:** Skill markdown 文件修改，无代码改动

## Global Constraints

- 输出仍然是一句完整的话，不要分段
- 输出风格：自然、真诚、有力量
- 不要用"哈哈"之类的无意义填充词
- 节假日优先级最高，月周期次之，星期特性最次
- 使用 `cyeam date holiday` 获取节假日信息，不再使用 curl

---

## Task 1: 更新 SKILL.md 的概述和数据来源

**Files:**
- Modify: `/Users/mnhkahn/.claude/skills/daily-encouragement/SKILL.md:1-36`

**Interfaces:**
- Produces: 更新后的技能说明，包含新的数据来源（cyeam 替代 curl）

- [ ] **Step 1: 更新概述部分**

将第 9 行的概述改为：
```markdown
查询今天的日期信息，包括月周期（月初/月中/月末）、星期特性（周一/周三/周五）、节假日状态，生成一句包含时间感知、鼓励话语和鸡汤语录的完整句子。
```

- [ ] **Step 2: 更新步骤部分**

将第 17-24 行的步骤改为：
```markdown
## 步骤

1. 调用 `cyeam date holiday` 命令获取今天的节假日信息（星期、状态、节日名称）
2. 解析今天的日期，判断月周期：
   - 月初（1-5号）→ 新的一月，设定小目标
   - 月中（14-16号）→ 进度过半，检查完成情况
   - 月末（≥25号）→ 冲刺收尾，给本月一个漂亮收尾
3. 判断星期特性：
   - 周一 → 新的一周开始，打气
   - 周三 → 一周过半，坚持住
   - 周五 → 周末就在眼前，期待
4. 优先级判断：
   - 如果是节假日/周末 → 节日祝福（最高优先级）
   - 如果是工作日 → 组合月周期 + 星期特性，生成针对性鼓励语
5. 加上一句鸡汤语录
6. 输出**一句完整的话**
```

- [ ] **Step 3: 更新 API 部分为 cyeam 命令**

将第 26-35 行的 curl API 部分替换为：
```markdown
## 节假日查询方式

使用 cyeam-cli 命令查询，无需密钥：

```bash
cyeam date holiday                  # 今天
cyeam date holiday 2026-10-01       # 指定日期
```
```

- [ ] **Step 4: 验证文件修改正确**

查看文件确认修改已应用：
```bash
head -50 /Users/mnhkahn/.claude/skills/daily-encouragement/SKILL.md
```

- [ ] **Step 5: Commit**

```bash
git add /Users/mnhkahn/.claude/skills/daily-encouragement/SKILL.md
git commit -m "refactor(daily-encouragement): update data source to cyeam and add steps outline"
```

---

## Task 2: 添加 cyeam 输出结构和判断逻辑说明

**Files:**
- Modify: `/Users/mnhkahn/.claude/skills/daily-encouragement/SKILL.md:37-76`

**Interfaces:**
- Consumes: Task 1 中更新的步骤框架
- Produces: 完整的判断逻辑说明和示例输出

- [ ] **Step 1: 更新返回结构说明**

将原 API 返回结构部分替换为 cyeam 的输出格式：
```markdown
### cyeam 输出结构

```
日期: 2026-10-01
星期: 周四
状态: 休息日
名称: 国庆节
薪资倍数: 3
```

### 状态说明

| 状态 | 含义 | 鼓励方向 |
|------|------|----------|
| 工作日 | 普通上班日 | 💪 好好干 |
| 休息日 | 法定假日或周末 | 🎉 好好玩 |
| 周末休息 | 周末 | 🎉 好好玩 |
| 调休补班 | 调休安排的上班日 | 💪 好好干 |
```

- [ ] **Step 2: 添加月周期判断规则**

在状态说明后添加新的小节：
```markdown
## 月周期判断规则

| 周期 | 日期范围 | 话术方向 |
|------|---------|---------|
| 月初 | 1-5号 | 新的一月，设定小目标，开启新篇章 |
| 月中 | 14-16号 | 进度过半，检查完成情况，继续加油 |
| 月末 | ≥25号 | 冲刺收尾，给自己一个漂亮的收尾，不要留遗憾 |

## 星期特性判断规则

| 星期 | 话术方向 |
|------|---------|
| 周一 | 新的一周开始，打气，仪式感 |
| 周三 | 一周过半，坚持住，再努力一下 |
| 周五 | 周末就在眼前，坚持完今天，期待周末 |
| 周二/周四 | 无特殊处理，沿用通用话术 |

## 优先级规则

1. 节假日/周末 → 最高优先级，按节日祝福逻辑
2. 月周期（月初/月中/月末）→ 第二优先级
3. 星期特性（周一/周三/周五）→ 第三优先级

多个维度同时触发时自然组合，例如：
- 周一 + 月初 → "今天是周一，也是X月的第一天，新的一周新的一月，双重开启！"
- 周五 + 月末 → "今天是周五，也是月底了，坚持完今天既能迎接周末又能给本月收尾！"
```

- [ ] **Step 3: 更新示例输出**

替换原示例输出为新的示例：
```markdown
## 示例输出

**周一工作日示例：**
> 今天是周一，新的一周开始啦！打起精神来 💪 每一个不曾起舞的日子，都是对生命的辜负。——早安！

**周三工作日示例：**
> 今天是周三，一周进度过半，坚持住！✊ 那些看似不起波澜的日复一日，会在某天让你看到坚持的意义。——加油！

**周五工作日示例：**
> 今天是周五，坚持完今天就是周末了！🎉 你今天的努力，是明天惊喜的铺垫。——期待周末！

**月初工作日示例：**
> 今天是7月的第一天，下半年正式开启！给自己设定一个小目标吧 🎯 种一棵树最好的时间是十年前，其次是现在。——开始行动！

**月末+周一示例：**
> 今天是周一，也是6月的最后一天，给这个月一个漂亮的收尾吧 ✨ 每一个不曾起舞的日子，都是对生命的辜负。——加油！

**节假日示例：**
> 今天是国庆节，放假啦！好好享受假期吧 🎉 生活不止眼前的苟且，还有诗和远方。——去探索吧！
```

- [ ] **Step 4: 验证文件修改正确**

查看文件确认修改已应用：
```bash
cat /Users/mnhkahn/.claude/skills/daily-encouragement/SKILL.md
```

- [ ] **Step 5: Commit**

```bash
git add /Users/mnhkahn/.claude/skills/daily-encouragement/SKILL.md
git commit -m "feat(daily-encouragement): add month cycle and weekday logic with examples"
```

---

## Task 3: 手动验证技能效果

**Files:**
- None (纯验证步骤)

**Interfaces:**
- Consumes: Task 1 和 Task 2 完成的 SKILL.md

- [ ] **Step 1: 验证 cyeam 命令可用**

运行：
```bash
which cyeam && cyeam date holiday
```
Expected: 输出今天的节假日信息

- [ ] **Step 2: 验证 skill 可以正常调用**

在 Claude 中输入："给我今天的鼓励"
Expected: 生成一句符合当天日期特性的鼓励语

- [ ] **Step 3: 验证不同日期场景（可选，使用指定日期参数）**

可以手动测试几个特殊日期：
- 月初（如 7月1日）：应该包含月初话术
- 月末（如 6月30日）：应该包含月末话术
- 周五：应该包含周五话术

- [ ] **Step 4: 验证通过后提交最终 commit（如果有额外修改）**

如果验证过程中发现需要调整的地方，修改后提交：
```bash
git add /Users/mnhkahn/.claude/skills/daily-encouragement/SKILL.md
git commit -m "fix(daily-encouragement): adjust wording based on validation"
```

---

## Plan Self-Review ✅

**1. Spec coverage:** 
- ✅ cyeam 替代 curl → Task 1 Step 2
- ✅ 月周期判断 → Task 2 Step 2
- ✅ 星期特性判断 → Task 2 Step 2
- ✅ 优先级规则 → Task 2 Step 2
- ✅ 组合规则 → Task 2 Step 2
- ✅ 示例输出 → Task 2 Step 3
- ✅ 输出格式保持 → Global Constraints

**2. Placeholder scan:** 无 TBD/TODO，所有步骤都有具体代码和命令

**3. Type consistency:** 所有文件路径正确，任务之间依赖关系清晰
