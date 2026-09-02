package money

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Scale is the number of nano-points in one point. All persisted balances and
// prices use this fixed scale so settlement never depends on floating point.
const Scale int64 = 1_000_000_000

type Amount int64

var ErrInvalidAmount = errors.New("invalid point amount")

func Parse(value string) (Amount, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, ErrInvalidAmount
	}

	negative := false
	if value[0] == '-' {
		negative = true
		value = value[1:]
	} else if value[0] == '+' {
		value = value[1:]
	}
	if value == "" {
		return 0, ErrInvalidAmount
	}

	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, ErrInvalidAmount
	}
	whole, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, ErrInvalidAmount
	}

	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > 9 {
			return 0, ErrInvalidAmount
		}
	}
	for len(fraction) < 9 {
		fraction += "0"
	}
	fractionValue := uint64(0)
	if fraction != "" {
		fractionValue, err = strconv.ParseUint(fraction, 10, 64)
		if err != nil {
			return 0, ErrInvalidAmount
		}
	}

	limit := uint64(math.MaxInt64)
	if negative {
		limit++
	}
	if whole > limit/uint64(Scale) {
		return 0, ErrInvalidAmount
	}
	magnitude := whole*uint64(Scale) + fractionValue
	if magnitude > limit {
		return 0, ErrInvalidAmount
	}
	if negative {
		if magnitude == uint64(math.MaxInt64)+1 {
			return Amount(math.MinInt64), nil
		}
		return Amount(-int64(magnitude)), nil
	}
	return Amount(magnitude), nil
}

func FromNano(value int64) Amount {
	return Amount(value)
}

func (a Amount) Nano() int64 {
	return int64(a)
}

func (a Amount) String() string {
	value := int64(a)
	sign := ""
	magnitude := uint64(value)
	if value < 0 {
		sign = "-"
		magnitude = uint64(-(value + 1)) + 1
	}
	whole := magnitude / uint64(Scale)
	fraction := magnitude % uint64(Scale)
	if fraction == 0 {
		return sign + strconv.FormatUint(whole, 10)
	}
	formatted := fmt.Sprintf("%09d", fraction)
	formatted = strings.TrimRight(formatted, "0")
	return fmt.Sprintf("%s%d.%s", sign, whole, formatted)
}
