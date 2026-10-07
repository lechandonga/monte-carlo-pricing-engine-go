# monte-carlo-pricing-engine-go

完全本地运行的期权定价与风险测算库，供研究同学在回测前做定价与敏感性分析。
不接任何外部行情或服务：定价参数由调用方显式传入，随机数全部由本地种子生成。

## 支持的产品

所有产品走同一入口 `Engine.Price` / `Engine.PriceBatch` / `Engine.PriceBatchTasks`：

| 产品 | `OptionType` | 说明 |
| --- | --- | --- |
| 欧式 | `European` | 香草看涨/看跌；解析基准为 Black-Scholes（`BlackScholes`） |
| 亚式 | `Asian` | 算术平均，定盘日由观察日历决定 |
| 障碍 | `Barrier` | 单障碍敲入/敲出，方向 Up/Down，离散日历或连续监测 |
| 回望 | `Lookback` | 浮动/固定行权价的看涨看跌，离散日历或连续监测 |

标的动态为几何布朗运动（GBM），参数：`Spot`、`Strike`、`Maturity`（年）、
`Rate`、`DivYield`（均为连续复利）、`Vol`。回望由 `LookbackSpec{Kind, Monitoring}`
描述，极值从期初 `Spot` 开始累计。

## 观察日历（`Option.ObservationDates`）

`ObservationDates []float64` 是一串以年为单位、严格递增的观察时点，用于：

- 亚式：只在日历列出的时点取均价；
- 离散障碍（`MonitorDiscrete`）：只在日历时点判断是否触障；
- 离散回望（`MonitorDiscrete`）：只在日历时点更新极值。

约束：

- 落在 `[0, Maturity]` 区间内；
- 必须严格递增、不允许重复；
- 最后一个时点必须恰好等于 `Maturity`（结算日一定观察）；
- 非法数值（NaN/Inf、负值、超期、乱序、重复、未含到期日）一律以
  `ErrInvalidParam` 直接拒绝，报错信息指明第几个日期和具体原因；
  **不会**自动排序、去重或取整。
- 传 `nil` 表示沿用旧的等间距口径：亚式为 `0, dt, ..., T`（含期初），
  障碍/回望为 `dt, ..., T`；欧式忽略该字段。
- 传非 nil 的空切片属于非法日历，直接拒绝。

模拟时把等间距数值网格与观察日历取并集，每段使用精确 GBM 增量，
因此日历上的时点不需要落在任何模拟步上，也不会被取整。
**`NumSteps` 只影响数值精度，不改变观察口径**。同一日历用不同步数定价，
结果只相差统计噪声（有测试保证）。

边界：日历只剩到期日一个观察点时，亚式只有一次终值定盘，价格与同行权价
欧式一致（测试 `TestNonUniformCalendarAsian`）。

## 离散与连续监测：收敛与容差语义

- `MonitorDiscrete`：观察严格按日历发生。
- `MonitorContinuous`：在每条模拟段内用 Brownian bridge 处理
  （障碍穿越概率 / 回望条件极值的无迭代精确抽样），结果对步长不敏感，
  即使只有 2 步也与连续闭式解在标准误以内。

理论基准（均在本包内，可直接调用）：

- `BlackScholes`：欧式；
- `ContinuousBarrier`：连续监测单障碍（Reiner–Rubinstein/Haug 闭式解，
  覆盖敲入/敲出 × 上/下 × 涨/跌，零返还金）；
- `ContinuousFloatingLookback`：连续监测浮动行权价回望
  （Goldman–Sosin–Gatto/Haug，含 r=q 可去奇点处理）。

收敛测试（`-v` 每档都打印价格与标准误）：

- `TestContinuousBarrierVsAnalytic`：2/16/128 步三档，每档与闭式解的偏差
  都必须落在 **3 倍该档自身标准误**以内——容差永远不会比标准误更松；
- `TestDiscreteCalendarConvergesToContinuous`：离散日历 4→256 个观察点，
  差距随加密持续单调收窄且方向正确。注意离散→连续的修正按
  Broadie–Glasserman–Kou 的 1/√m 缓慢衰减，256 个观察点仍可统计区分，
  这是理论预期而非数值问题；
- `TestContinuousFloatingLookbackVsAnalytic`、
  `TestLookbackDiscreteConvergesToContinuous`：回望的同样口径检查。

口径收紧方向（超出统计误差的违例算失败，均有测试）：

- 连续监测的敲出 **不高于**离散监测；
- 连续监测的敲入 **不低于**离散监测；
- 连续监测回望 **不低于**离散监测回望。

## 回望期权的硬关系

- 固定行权价回望 ≥ 同行权价、同方向欧式；
- 浮动行权价回望 ≥ 行权价取期初价的同方向欧式；
- 连续监测 ≥ 离散监测；
- 对偶开关（`Antithetic`）打开后标准误显著下降、价格不跑偏。

## 参数校验与错误分类

所有错误都是 `*mcengine.Error`，含 `Kind` 字段，可用 `errors.As` 区分：

| Kind | 含义 |
| --- | --- |
| `ErrInvalidParam` | 参数非法（含非法日历、非法回望/障碍配置） |
| `ErrTooFewPaths` | `NumPaths` 低于 `MinPaths`（默认 1000） |
| `ErrNotConverged` | 设置了 `RelTol` 且 `StdErr/|Price|` 超过它 |
| `ErrNonFinite` | 模拟中出现 NaN/Inf |
| `ErrCanceled` | 批量在该合约开始定价前被取消 |

## 误差、方差缩减与希腊值

- `Result` 返回 `Price`、`StdErr` 与 95% 置信区间 `[CILow, CIHigh]`。
- `Config.Antithetic` 开启对偶变量：同一组正态增量的正负两条路径，
  bridge 均匀数同步镜像，保证对偶配对精确。
- 希腊值 `Engine.Greeks`（Delta/Gamma/Vega/Theta/Rho）用公共随机数中心差分；
  对带日历的合约，扰动到期日时日历等比缩放，同一输入反复计算逐位一致；
  回望产品同样覆盖。

## 批量定价：真实回测规模

- `PriceBatchTasks(ctx, []BatchTask)` 是回测入口：每个 `BatchTask`
  自带 `Option`（含各自日历）和 `Config`（路径数、步数、对偶、限差都可不同）。
- 单个 `Price` 内部也按 `Config.MaxWorkers` 并行；随机数按
  `(路径, 段, 类型)` 可寻址生成，每路径收益写入预分配槽位后按固定顺序归约，
  因此**结果只取决于该合约自身输入**，与 worker 数、任务顺序、邻居配置无关，
  任意并行度下逐位一致（测试覆盖 1 vs 8 worker、乱序、邻居改动）。
- 单个合约非法或算不出来时**只让它自己失败**，错误带可分类 `Kind`，
  其余合约照常出结果。
- `ctx` 取消后尽快停止派发新任务；在飞合约跑完（其结果仍可用），
  未开始的合约返回 `ErrCanceled`；已完成结果与不取消的全量运行逐位一致。
- CPU 占用受 `MaxWorkers` 限制（0 = NumCPU）；1500 个观察点的合约
  内存只与 `NumSteps + len(calendar)` 成正比（测试内 4 万路径约 1MB 级）。
- `PriceBatch([]Option, Config)` 保留为旧的全表共用配置入口，内部转发到
  `PriceBatchTasks`。

## 本地验证

```bash
go test ./...                                     # 全量测试
go test -v ./...                                  # 查看业务/用例与每档价格、标准误日志
CGO_ENABLED=1 go test -race -run 'TestDeterminismAcrossWorkers|TestBatch|TestLargeBatchDeterminism|TestLookbackHardRelations' ./...
```

测试按业务分组（`calendar_test.go` 日历与障碍收敛、`lookback_test.go`
回望、`batch_test.go` 批量隔离与取消、`scale_test.go` 大规模、
`engine_test.go` 原有产品与希腊值），`-v` 输出可直接看到每个业务下的
用例名与定价/标准误结果。

## 快速上手

```go
eng := mcengine.NewEngine()
opt := mcengine.Option{
    Type: mcengine.Asian, Payoff: mcengine.Call,
    Spot: 100, Strike: 100, Maturity: 1,
    Rate: 0.05, DivYield: 0.02, Vol: 0.2,
    ObservationDates: []float64{0.05, 0.25, 0.3, 0.7, 1.0},
}
res, err := eng.Price(opt, mcengine.Config{
    NumPaths: 100000, NumSteps: 128, Seed: 42,
    Antithetic: true, RelTol: 0.005,
})

// 真实回测：几百个各自带日历与配置的合约，可中途取消
tasks := []mcengine.BatchTask{{Option: opt, Config: resCfg}}
results := eng.PriceBatchTasks(ctx, tasks) // results[i].Err 为 *mcengine.Error
```
