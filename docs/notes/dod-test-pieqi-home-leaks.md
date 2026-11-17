# test 票 DoD 把 PIEQI_HOME 泄漏给外项目

| 字段 | 值 |
|---|---|
| 状态 | **已落地**（2026-11-17） |
| 相关 | [requirement-delivery.md](requirement-delivery.md)；Pack `dod_test` / `AppendDeliveryDoDFrom` |
| 样本 | 外项目（如 viomi-warehouse）test 票 plan 出现「设置 PIEQI_HOME/port 并构建运行」 |

## 现象

外项目 test 票 plan 把 pieqi 自测环境变量当通项照抄，与被测项目无关。

注入路径：`AppendDeliveryDoDFrom` ← Pack `dod_test`（如 `s1-plan-serial.yaml`；空则回落 `deliveryDoDTest`）。

## 根因

交付纪律「另起隔离实例」正确，但举例写死成「pieqi：独立 PIEQI_HOME + port」通项；agent 照抄进 plan。

## 目标

1. 通用 DoD 只要求：检出需求分支 → 编译 → 另起**该项目自己的**隔离运行环境验收（端口/配置/HOME 由目标项目决定）。
2. `PIEQI_HOME` / `PIEQI_SERVER_PORT` / `:3000` 仅作为「若本仓库即 pieqi」特例出现，不作外项目通项。
3. implement DoD「禁止起 pieqi 服务当完成条件」同理：外项目写「禁止起被测服务」；pieqi 特例另标。

## 落点（本票：A + C）

| 方案 | 做法 | 本票 |
|---|---|---|
| **A** | 改 packs + 内置回落：通项去 PIEQI_*；加「若本仓库即 pieqi，则用独立 PIEQI_HOME+port」 | ✅ |
| B | 按 `project_path` 分支注入（加固） | 未做；需要时再加 |
| **C** | 段名 `pieqi delivery` / `pieqi 交付纪律` → 中性 `delivery` / `交付纪律` | ✅ |

幂等：旧标记（`pieqi 交付纪律` / `pieqi delivery`）与新标记均视为已注入，不重复追加。旧票 prompt **不**自动改写。

## 非目标

- 不取消 test 必须隔离起服务
- 不改 test failed → Issue / 总评 FAIL 门禁
- 不改状态机

## 验收

1. 非 pieqi 项目新落地 test 票：通项不把 PIEQI_* / `:3000` 当必做步骤（仅条件特例句）
2. pieqi 自测仍有独立 PIEQI_HOME+port 指引（条件句）
3. 「隔离起服务 / isolated」纪律文案仍在
