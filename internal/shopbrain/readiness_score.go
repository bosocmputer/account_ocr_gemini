package shopbrain

import (
	"fmt"
	"math"
	"time"
)

// ComputeReadiness is "สมองพร้อมใช้งาน X%" — the same four conditions and
// formulas as the shop-brain page (bcaccount src/utils/shopBrainReadiness.js),
// so an MCP client sees the number the owner sees.
//
//	data     = (min(1, journals ÷ MinJournals) + fresh?1:0) ÷ 2
//	accuracy = bills answered < MinMeasured → 0; else allowed error ÷ real error (≤ 1)
//	coverage = bills < MinMeasured → 0; else min(1, coverage ÷ TargetCoverage)
//	verdicts = min(1, owner checks ÷ TargetVerdicts)
//
// Bills = backtest by_source.image. A shop without bills skips accuracy and
// coverage; a brand-new shop counts only data. Score = mean of counted × 100.

type ReadinessCondition struct {
	Key      string  `json:"key"`
	Progress float64 `json:"progress"` // 0..1
	Counted  bool    `json:"counted"`
	Measured bool    `json:"measured"`
	Have     string  `json:"have"`
	Target   string  `json:"target"`
}

type ReadinessScore struct {
	Score      *int                 `json:"score"` // nil when nothing can be counted
	Conditions []ReadinessCondition `json:"conditions"`
}

func (r ReadinessScore) ScoreText() string {
	if r.Score == nil {
		return "-"
	}
	return fmt.Sprintf("%d%%", *r.Score)
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func ComputeReadiness(b *Brain, verdicts int, verdictsKnown bool, now time.Time) ReadinessScore {
	o := b.Options
	r := b.Readiness
	bills := HitStat{}
	if h := b.Backtest.BySource[SourceImage]; h != nil {
		bills = *h
	}
	isNew := r.Journals == 0
	var cs []ReadinessCondition

	fresh := !r.LastEntry.IsZero() && now.Sub(r.LastEntry) <= time.Duration(o.MaxIdleDays)*24*time.Hour
	data := clamp01(float64(r.Journals) / float64(max(o.MinJournals, 1)))
	if fresh {
		data = (data + 1) / 2
	} else {
		data /= 2
	}
	cs = append(cs, ReadinessCondition{Key: "data", Progress: data, Counted: true, Measured: true,
		Have: fmt.Sprintf("%d รายการ", r.Journals), Target: fmt.Sprintf("≥ %d รายการ และมีรายการใหม่ใน %d วัน", o.MinJournals, o.MaxIdleDays)})

	switch {
	case bills.Total == 0:
		cs = append(cs, ReadinessCondition{Key: "accuracy", Have: "ไม่มีบิลที่มีรูป"})
		cs = append(cs, ReadinessCondition{Key: "coverage", Have: "ไม่มีบิลที่มีรูป"})
	default:
		acc := ReadinessCondition{Key: "accuracy", Counted: true, Target: fmt.Sprintf("ถูก ≥ %.0f%%", o.TargetAccuracy*100)}
		if bills.Predicted >= o.MinMeasured {
			err := 1 - float64(bills.HitAccounts)/float64(bills.Predicted)
			allowed := 1 - o.TargetAccuracy
			acc.Measured = true
			acc.Progress = 1
			if err > allowed {
				acc.Progress = clamp01(allowed / err)
			}
			acc.Have = fmt.Sprintf("ถูก %.1f%% (%d/%d ใบที่ตอบ)", (1-err)*100, bills.HitAccounts, bills.Predicted)
		} else {
			acc.Have = fmt.Sprintf("ยังวัดไม่ได้ — ตอบบิลได้ %d ใบ (ต้อง ≥ %d)", bills.Predicted, o.MinMeasured)
		}
		cov := ReadinessCondition{Key: "coverage", Counted: true, Target: fmt.Sprintf("≥ %.0f%% ของบิล", o.TargetCoverage*100)}
		if bills.Total >= o.MinMeasured {
			c := float64(bills.Predicted) / float64(bills.Total)
			cov.Measured = true
			cov.Progress = clamp01(c / o.TargetCoverage)
			cov.Have = fmt.Sprintf("ตอบได้ %.0f%% (%d/%d ใบ)", c*100, bills.Predicted, bills.Total)
		} else {
			cov.Have = fmt.Sprintf("ยังวัดไม่ได้ — มีบิล %d ใบ (ต้อง ≥ %d)", bills.Total, o.MinMeasured)
		}
		cs = append(cs, acc, cov)
	}

	v := ReadinessCondition{Key: "verdicts", Target: fmt.Sprintf("≥ %d ข้อ", o.TargetVerdicts)}
	if isNew {
		v.Have = "ยังไม่มีรายการให้ตรวจ"
	} else {
		v.Counted = true
		v.Measured = verdictsKnown
		if verdictsKnown {
			v.Progress = clamp01(float64(verdicts) / float64(max(o.TargetVerdicts, 1)))
			v.Have = fmt.Sprintf("%d ข้อ", verdicts)
		} else {
			v.Have = "ยังนับไม่ได้ (ที่เก็บคำตอบไม่พร้อม)"
		}
	}
	cs = append(cs, v)

	sum, n := 0.0, 0
	for _, c := range cs {
		if c.Counted {
			sum += c.Progress
			n++
		}
	}
	out := ReadinessScore{Conditions: cs}
	if n > 0 {
		s := int(math.Round(sum / float64(n) * 100))
		out.Score = &s
	}
	return out
}
