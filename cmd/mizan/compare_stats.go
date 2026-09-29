// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

// Statistics for compare-engines: accuracy against gold labels with bootstrap
// confidence intervals, paired McNemar tests, Cohen's kappa, rank correlation
// for Likert scores, and calibration (ECE / Brier) where an engine reports a
// confidence. Everything here is pure and deterministic (seeded bootstrap).

import (
	"math"
	"math/rand"
	"sort"
)

// Interval is a two-sided confidence interval.
type Interval struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// bootstrapMeanCI returns the percentile bootstrap 95% CI of the mean of xs.
func bootstrapMeanCI(xs []float64, iters int, seed int64) Interval {
	n := len(xs)
	if n == 0 || iters <= 0 {
		return Interval{}
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // G404: seeded bootstrap must be reproducible, not cryptographic
	means := make([]float64, iters)
	for i := range means {
		var s float64
		for j := 0; j < n; j++ {
			s += xs[rng.Intn(n)]
		}
		means[i] = s / float64(n)
	}
	sort.Float64s(means)
	return Interval{Lo: quantileSorted(means, 0.025), Hi: quantileSorted(means, 0.975)}
}

// bootstrapDiffCI returns the paired bootstrap 95% CI of mean(a) - mean(b).
func bootstrapDiffCI(a, b []float64, iters int, seed int64) Interval {
	n := len(a)
	if n == 0 || n != len(b) || iters <= 0 {
		return Interval{}
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // G404: seeded bootstrap must be reproducible, not cryptographic
	diffs := make([]float64, iters)
	for i := range diffs {
		var s float64
		for j := 0; j < n; j++ {
			k := rng.Intn(n)
			s += a[k] - b[k]
		}
		diffs[i] = s / float64(n)
	}
	sort.Float64s(diffs)
	return Interval{Lo: quantileSorted(diffs, 0.025), Hi: quantileSorted(diffs, 0.975)}
}

func quantileSorted(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	pos := q * float64(len(xs)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return xs[lo]
	}
	return xs[lo] + (xs[hi]-xs[lo])*(pos-float64(lo))
}

func percentile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	return quantileSorted(c, q)
}

// mcnemarExact returns the two-sided exact McNemar p-value for b (A right, B
// wrong) and c (A wrong, B right) discordant pairs.
func mcnemarExact(b, c int) float64 {
	n := b + c
	if n == 0 {
		return 1
	}
	k := b
	if c < k {
		k = c
	}
	// P(X <= k) for X ~ Binomial(n, 0.5), doubled.
	var p float64
	for i := 0; i <= k; i++ {
		p += math.Exp(lnChoose(n, i) - float64(n)*math.Ln2)
	}
	p *= 2
	if p > 1 {
		p = 1
	}
	return p
}

func lnChoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}

// cohenKappa computes Cohen's kappa between two raters' categorical labels.
func cohenKappa(a, b []string) float64 {
	n := len(a)
	if n == 0 || n != len(b) {
		return math.NaN()
	}
	var agree float64
	ca := map[string]float64{}
	cb := map[string]float64{}
	for i := range a {
		if a[i] == b[i] {
			agree++
		}
		ca[a[i]]++
		cb[b[i]]++
	}
	po := agree / float64(n)
	var pe float64
	for k, v := range ca {
		pe += (v / float64(n)) * (cb[k] / float64(n))
	}
	if pe >= 1 {
		return 1
	}
	return (po - pe) / (1 - pe)
}

// spearman returns Spearman's rank correlation (average ranks for ties).
func spearman(x, y []float64) float64 {
	if len(x) < 3 || len(x) != len(y) {
		return math.NaN()
	}
	return pearson(ranks(x), ranks(y))
}

func ranks(xs []float64) []float64 {
	idx := make([]int, len(xs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return xs[idx[i]] < xs[idx[j]] })
	r := make([]float64, len(xs))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && xs[idx[j+1]] == xs[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			r[idx[k]] = avg
		}
		i = j + 1
	}
	return r
}

func pearson(x, y []float64) float64 {
	n := float64(len(x))
	if n < 2 {
		return math.NaN()
	}
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= n
	my /= n
	var sxy, sxx, syy float64
	for i := range x {
		dx, dy := x[i]-mx, y[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}

// ece10 is the 10-bin expected calibration error of top-1 confidence vs correctness.
func ece10(conf []float64, correct []bool) float64 {
	if len(conf) == 0 {
		return math.NaN()
	}
	binN := make([]float64, 10)
	binC := make([]float64, 10)
	binA := make([]float64, 10)
	for i, c := range conf {
		b := min(max(int(c*10), 0), 9)
		binN[b]++
		binC[b] += c
		if correct[i] {
			binA[b]++
		}
	}
	var e float64
	n := float64(len(conf))
	for b := 0; b < 10; b++ {
		if binN[b] == 0 {
			continue
		}
		e += binN[b] / n * math.Abs(binA[b]/binN[b]-binC[b]/binN[b])
	}
	return e
}

// brierTop1 is the mean squared error of top-1 confidence vs correctness.
func brierTop1(conf []float64, correct []bool) float64 {
	if len(conf) == 0 {
		return math.NaN()
	}
	var s float64
	for i, c := range conf {
		y := 0.0
		if correct[i] {
			y = 1
		}
		s += (c - y) * (c - y)
	}
	return s / float64(len(conf))
}

// finite replaces NaN/Inf with nil so reports marshal to valid JSON.
func finite(f float64) *float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}
