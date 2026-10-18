package fingerprint

import (
	"math"
	"regexp"
	"strconv"
	"unicode"
)

var digits = regexp.MustCompile(`[0-9]+`)

// ParseNumbers deliberately mirrors the pinned parser, including accepting the
// positive digits in "-1". Letters delimit runs; the first longest run wins.
func ParseNumbers(text string) []int {
	var best, current []int
	previous := 0
	for _, loc := range digits.FindAllStringIndex(text, -1) {
		if len(current) > 0 {
			for _, r := range text[previous:loc[0]] {
				if unicode.IsLetter(r) {
					if len(current) > len(best) {
						best = current
					}
					current = nil
					break
				}
			}
		}
		n, err := strconv.Atoi(text[loc[0]:loc[1]])
		if err == nil && n >= 1 && n <= 355 {
			current = append(current, n)
		}
		previous = loc[1]
	}
	if len(current) > len(best) {
		best = current
	}
	return best
}
func dot(a, b vector) float64 {
	var out float64
	for i, x := range a {
		out += x * b[i]
	}
	return out
}
func mean(a vector) float64 {
	var out float64
	for _, x := range a {
		out += x
	}
	return out / float64(len(a))
}
func normalize(a vector) vector {
	length := math.Max(math.Sqrt(dot(a, a)), 1e-12)
	out := make(vector, len(a))
	for i, x := range a {
		out[i] = x / length
	}
	return out
}
func z(a vector) vector {
	m := mean(a)
	var variance float64
	for _, x := range a {
		variance += (x - m) * (x - m)
	}
	sd := math.Max(math.Sqrt(variance/float64(len(a))), 1e-12)
	out := make(vector, len(a))
	for i, x := range a {
		out[i] = (x - m) / sd
	}
	return out
}
func columnMean(a matrix) vector {
	out := make(vector, len(a[0]))
	for i := range out {
		column := make(vector, len(a))
		for j, row := range a {
			column[j] = row[i]
		}
		out[i] = mean(column)
	}
	return out
}
func subtract(v vector, basis matrix) vector {
	coeff := make(vector, len(basis))
	for j, row := range basis {
		coeff[j] = dot(v, row)
	}
	out := make(vector, len(v))
	for i, x := range v {
		var sum float64
		for j, row := range basis {
			sum += coeff[j] * row[i]
		}
		out[i] = x - sum
	}
	return out
}
func hellinger(numbers []int) vector {
	counts := make(vector, 355)
	for _, n := range numbers {
		counts[n-1]++
	}
	denominator := float64(len(numbers)) + .5*355
	for i, x := range counts {
		counts[i] = math.Sqrt((x + .5) / denominator)
	}
	return counts
}
func ordered(numbers []int) vector {
	out := make(vector, 0, 74)
	start := 0
	for block := range 4 {
		size := len(numbers) / 4
		if block < len(numbers)%4 {
			size++
		}
		bins := make(vector, 16)
		for i := range bins {
			bins[i] = .5
		}
		for _, n := range numbers[start : start+size] {
			bins[min(15, int(float64(n-1)/355*16))]++
		}
		for _, x := range bins {
			out = append(out, math.Sqrt(x/float64(size+8)))
		}
		start += size
	}
	digits := make(vector, 10)
	for i := range digits {
		digits[i] = .5
	}
	for _, n := range numbers {
		digits[n%10]++
	}
	for _, x := range digits {
		out = append(out, math.Sqrt(x/(float64(len(numbers))+5)))
	}
	return out
}
func positionKernel(numbers []int) vector {
	out := make(vector, 102)
	n := float64(len(numbers))
	for pos, value := range numbers {
		t := (float64(pos)+.5)*2/n - 1
		basis := vector{t, .5 * (3*t*t - 1), .5 * (5*math.Pow(t, 3) - 3*t)}
		for k := range 34 {
			var weight float64
			if k < 24 {
				centre := 1 + float64(k)*354/23
				weight = math.Exp(-.5 * math.Pow((float64(value)-centre)/20, 2))
			} else if k-24 == value%10 {
				weight = 1
			}
			for j, b := range basis {
				out[k*3+j] += weight * b / n
			}
		}
	}
	return out
}
func transitions(symbols []int, bins int) vector {
	joint := make(vector, bins*bins)
	for i := range joint {
		joint[i] = .5
	}
	for i := 1; i < len(symbols); i++ {
		joint[symbols[i-1]*bins+symbols[i]]++
	}
	total := float64(len(symbols)-1) + .5*float64(bins*bins)
	rows, columns := make(vector, bins), make(vector, bins)
	for i := range joint {
		joint[i] /= total
		rows[i/bins] += joint[i]
	}
	for b := range bins {
		for a := range bins {
			columns[b] += joint[a*bins+b]
		}
	}
	for i := range joint {
		joint[i] -= rows[i/bins] * columns[i%bins]
	}
	return joint
}
func fraction(n int, test func(int) bool) float64 {
	hits := 0
	for i := range n {
		if test(i) {
			hits++
		}
	}
	return float64(hits) / float64(n)
}
func sign(n int) int {
	if n > 0 {
		return 1
	}
	if n < 0 {
		return -1
	}
	return 0
}
func sequence(numbers []int) vector {
	n := len(numbers)
	values := make(vector, n)
	for i, x := range numbers {
		values[i] = float64(x)
	}
	m := mean(values)
	var variance float64
	for _, v := range values {
		variance += (v - m) * (v - m)
	}
	sd := math.Max(math.Sqrt(variance/float64(n)), 1)
	s := make(vector, n)
	for i, v := range values {
		s[i] = (v - m) / sd
	}
	out := make(vector, 0, 220)
	for _, lag := range []int{1, 2, 3, 5, 8, 13, 21, 34} {
		count := n - lag
		delta, corr, abs := make(vector, count), make(vector, count), make(vector, count)
		for i := range count {
			delta[i] = values[i+lag] - values[i]
			corr[i] = s[i+lag] * s[i]
			abs[i] = math.Abs(delta[i])
		}
		out = append(out, mean(corr), mean(abs)/355, fraction(count, func(i int) bool { return delta[i] > 0 }), fraction(count, func(i int) bool { return delta[i] == 0 }), fraction(count, func(i int) bool { return abs[i] <= 5 }), fraction(count, func(i int) bool { return abs[i] <= 20 }))
	}
	buckets, digits := make([]int, n), make([]int, n)
	for i, v := range numbers {
		buckets[i] = min(7, (v-1)*8/355)
		digits[i] = v % 10
	}
	out = append(out, transitions(buckets, 8)...)
	out = append(out, transitions(digits, 10)...)
	diff := make([]int, n-1)
	steps := make(vector, n-1)
	for i := range n - 1 {
		diff[i] = numbers[i+1] - numbers[i]
		steps[i] = math.Abs(s[i+1] - s[i])
	}
	out = append(out, fraction(n-2, func(i int) bool { return sign(diff[i+1]) == sign(diff[i]) }), fraction(n-2, func(i int) bool { return diff[i+1] == diff[i] }), fraction(n-1, func(i int) bool { return digits[i] == digits[i+1] }), fraction(n-1, func(i int) bool { return steps[i] > 1 }), fraction(n-1, func(i int) bool { return steps[i] > 2 }))
	for i, v := range numbers {
		buckets[i] = min(15, (v-1)*16/355)
	}
	for _, width := range []int{4, 8, 16} {
		var sum float64
		for i := 0; i+width <= n; i++ {
			var seen [16]bool
			unique := 0
			for _, b := range buckets[i : i+width] {
				if !seen[b] {
					seen[b] = true
					unique++
				}
			}
			sum += float64(unique) / float64(width)
		}
		out = append(out, sum/float64(n-width+1))
	}
	return out
}
