# monte-carlo-pricing-engine-go

完全本地运行的期权定价与风险测算库，供研究同学在回测前做定价与敏感性分析。
不接任何外部行情或服务：定价参数由调用方显式传入，随机数全部由本地种子生成。

## 支持的产品

所有产品走同一入口 `Engine.Price` / `Engine.PriceBatch`：

| 产品 | `OptionType` | 说明 |
| --- | --- | --- |
| 欧式 | `European` | 香草看涨/看跌；解析基准为 Black-Scholes（`BlackScholes`） |
| 亚式 | `Asian` | 算术平均，固定在每个模拟步上取值（离散定盘） |
| 障碍 | `Barrier` | 单障碍敲入/敲出，方向 Up/Down |

标的动态为几何布朗运动（GBM），参数：`Spot`、`Strike`、`Maturity`（年）、
`Rate`、`DivYield`（均为连续复利）、`Vol`。

### 障碍期权监测语义

- `MonitorDiscrete`：仅在 `NumSteps` 个等距模拟日期检查是否触障，步数越少障碍越"松"。
- `MonitorContinuous`：视为连续监测；相邻两个模拟点之间用 Brownian bridge
  穿越概率判定，步长细分后收敛到连续障碍价格。

同一组路径下连续监测的敲出期权价格不高于离散监测（触障更频繁），
敲入 + 敲出在同一随机数流上与对应欧式期权满足逐路径平价。

## 参数校验与错误分类

所有错误都是 `*mcengine.Error`，含 `Kind` 字段，可用 `errors.As` 区分：

| Kind | 含义 |
| --- | --- |
| `ErrInvalidParam` | 参数非法：负波动率、非正到期、非正标的价格/行权价、NaN/Inf、障碍价在标的价格错误一侧、障碍与行权价矛盾（如 up-and-out call 障碍 ≤ 行权价，永不可能赔付） |
| `ErrTooFewPaths` | `NumPaths` 低于 `MinPaths`（默认 1000） |
| `ErrNotConverged` | 设置了 `RelTol` 且 `StdErr/|Price|` 超过它 |
| `ErrNonFinite` | 模拟中出现 NaN/Inf（路径、系数或最终结果） |

非法参数一律直接拒绝，不会返回默认值或"看起来正常"的结果。

## 误差、收敛与方差缩减

- `Result` 返回 `Price`、`StdErr` 与 95% 置信区间 `[CILow, CIHigh]`。
- 欧式场景与 Black-Scholes 解析解在 3 倍标准误内一致（有测试保证）。
- 路径依赖产品（亚式、障碍）细分 `NumSteps` 后价格收敛（有测试保证）。
- `Config.Antithetic = true` 开启对偶变量方差缩减：标准误显著下降，
  价格与未开启时保持在统计容差内。
- 希腊值（`Engine.Greeks`：Delta/Gamma/Vega/Theta/Rho）用公共随机数
  （同一 seed 做基准与扰动）的中心差分估计，同一输入反复计算结果逐位一致。

## 可复现性与并行

- 单个 `Price` 的结果只取决于 `(Option, Config)`，与 goroutine 调度无关。
- `PriceBatch` 中每个任务的种子由 `cfg.Seed` 与期权内容哈希派生，
  因此任意 `MaxWorkers`、任意任务顺序下结果逐位一致；换种子结果才改变。
- `Config.MaxWorkers` 限制并行度（0 表示 NumCPU），用于控制资源占用。

## 本地验证

```bash
go test ./...                 # 全量测试
go test -v ./...              # 查看各业务用例与定价/误差日志
CGO_ENABLED=1 go test -race ./...   # 并发用例在 race 检测下验证
```

测试覆盖：非法参数与障碍矛盾、欧式 vs 解析解、亚式步长收敛、
障碍敲入敲出平价与监测语义、对偶方差缩减、并发/种子/顺序一致性、
路径数不足、不收敛、非有限数值、希腊值稳定性。

## 快速上手

```go
eng := mcengine.NewEngine()
opt := mcengine.Option{
    Type: mcengine.Asian, Payoff: mcengine.Call,
    Spot: 100, Strike: 100, Maturity: 1,
    Rate: 0.05, DivYield: 0.02, Vol: 0.2,
}
res, err := eng.Price(opt, mcengine.Config{
    NumPaths: 100000, NumSteps: 128, Seed: 42,
    Antithetic: true, RelTol: 0.005,
})
// err 为 *mcengine.Error，可按 Kind 分类处理
```
