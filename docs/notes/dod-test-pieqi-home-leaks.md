# test 票 DoD 把 PIEQI_HOME 泄漏给外项目

| 字段 | 值 |
|---|---|
| 状态 | **已收紧**（2026-11-18：通项与条件句均去产品术语） |
| 相关 | [requirement-delivery.md](requirement-delivery.md)；Pack `dod_test` / `AppendDeliveryDoDFrom` |
| 样本 | 外项目（如 viomi-warehouse）test 票 plan 出现「设置 PIEQI_HOME/port 并构建运行」 |

## 现象

外项目 test 票 plan 把编排层自测环境变量当通项照抄，与被测项目无关。

注入路径：`AppendDeliveryDoDFrom` ← Pack `dod_test`（如 `s1-plan-serial.yaml`；空则回落 `deliveryDoDTest`）。

## 根因

交付纪律「另起隔离实例」正确，但举例写死成「独立 PIEQI_HOME + port / :3000」；agent 照抄进 plan。

## 目标（现行）

1. 通用 DoD 只要求：检出需求分支 → 编译 → 另起**该项目自己的**隔离运行环境验收（端口/配置/数据目录由目标项目决定；勿占用现网实例）。
2. DoD / pack **不得**出现 `PIEQI_HOME` / `PIEQI_SERVER_PORT` / `:3000` / `~/.pieqi` / 「若本仓库即 pieqi」等编排层定制术语——无论通项还是条件特例。
3. implement DoD 只写「禁止起被测服务」，不点名具体产品服务。

编排层自身的隔离验收约定写在运维文档 / AGENTS，**不**注入外项目票面。

## 落点

| 方案 | 做法 | 状态 |
|---|---|---|
| A′ | packs + 内置回落：通项与条件句均去 PIEQI_* / :3000 | ✅ |
| C | 段名中性 `delivery` / `交付纪律` | ✅ |

幂等：旧标记（`pieqi 交付纪律` / `pieqi delivery`）与新标记均视为已注入。旧票 prompt **不**自动改写。

## 非目标

- 不取消 test 必须隔离起服务
- 不改 test failed → Issue / 总评 FAIL 门禁
- 不改状态机

## 验收

1. 新落地 test/implement 票 DoD：无 `PIEQI_*` / `:3000` / `~/.pieqi` / 「本仓库即 pieqi」
2. 「隔离起服务 / isolated / target project」纪律文案仍在
