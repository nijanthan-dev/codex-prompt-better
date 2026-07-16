package outlier

import (
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestRankPreservesNativeUnits(t *testing.T) {
	value, median, mad := 200.0, 100.0, 10.0
	seconds, secondsMedian, secondsMAD := 30.0, 20.0, 5.0
	result := Rank([]contracts.MetricResult{
		{Name: "tokens", NativeValue: &value, NativeUnit: "tokens/turn", RollingMedian: &median, RollingMAD: &mad},
		{Name: "runtime", NativeValue: &seconds, NativeUnit: "seconds/turn", RollingMedian: &secondsMedian, RollingMAD: &secondsMAD},
	})
	if len(result) != 2 || result[0].Metric != "tokens" ||
		result[0].NativeUnit != "tokens/turn" || result[1].NativeUnit != "seconds/turn" {
		t.Fatalf("result=%+v", result)
	}
}
