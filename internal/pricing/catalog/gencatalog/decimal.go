package main

import (
	"errors"
	"fmt"
	"strings"
)

// This file is the generator's exact decimal arithmetic. It exists so no
// float64 appears anywhere in the generator: a price is a string of digits
// and a decimal point, shifted and scaled by string arithmetic, so
// regenerating from the same inputs produces the same bytes and a diff
// shows a price change rather than a formatting wobble (design §4).

// shiftDecimal multiplies a non-negative decimal string by 10^places,
// exactly, and returns the canonical spelling: no leading zeros, no
// trailing zeros in the fraction and no trailing point. "0.00000002016"
// shifted six places is "0.02016"; "0.000000002" is "0.002"; "2" is
// "2000000".
func shiftDecimal(s string, places int) (string, error) {
	intPart, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if intPart == "" && frac == "" {
		return "", errors.New("it is not a decimal number")
	}
	if !digits(intPart) || !digits(frac) {
		return "", fmt.Errorf("%q is not a plain non-negative decimal number", s)
	}
	// The value is (intPart+frac) * 10^(places-len(frac)).
	d := strings.TrimLeft(intPart+frac, "0")
	if d == "" {
		return "0", nil
	}
	return render(d, len(frac)-places), nil
}

// scaleBy multiplies a canonical decimal string by num/den exactly. The
// factors the generator uses (1, 5/4 and 2) all terminate in decimal, so
// the result is exact; a factor that did not would be a programming error,
// not a rounding decision, so it is refused rather than rounded.
func scaleBy(s string, num, den int) (string, error) {
	if num <= 0 || den <= 0 {
		return "", fmt.Errorf("the factor %d/%d is not positive", num, den)
	}
	intPart, frac, _ := strings.Cut(s, ".")
	if !digits(intPart) || !digits(frac) {
		return "", fmt.Errorf("%q is not a plain non-negative decimal number", s)
	}
	d := strings.TrimLeft(intPart+frac, "0")
	if d == "" {
		return "0", nil
	}
	scale := len(frac)
	// Multiply by num, then divide by den by scaling the numerator until it
	// divides: den's only prime factors here are 2 and 5, so this
	// terminates.
	d = mulSmall(d, num)
	for {
		if r := modSmall(d, den); r != 0 {
			d, scale = mulSmall(d, 10), scale+1
			continue
		}
		break
	}
	return render(divSmall(d, den), scale), nil
}

// lessDecimal reports whether a < b for two canonical non-negative decimal
// strings.
func lessDecimal(a, b string) bool {
	ai, af, _ := strings.Cut(a, ".")
	bi, bf, _ := strings.Cut(b, ".")
	ai, bi = strings.TrimLeft(ai, "0"), strings.TrimLeft(bi, "0")
	if len(ai) != len(bi) {
		return len(ai) < len(bi) // no leading zeros: the longer integer part is bigger
	}
	if ai != bi {
		return ai < bi
	}
	n := max(len(af), len(bf))
	return af+strings.Repeat("0", n-len(af)) < bf+strings.Repeat("0", n-len(bf))
}

// render spells the value d * 10^-scale canonically.
func render(d string, scale int) string {
	d = strings.TrimLeft(d, "0")
	if d == "" {
		return "0"
	}
	if scale <= 0 {
		return d + strings.Repeat("0", -scale)
	}
	if len(d) <= scale {
		d = strings.Repeat("0", scale-len(d)+1) + d
	}
	out := d[:len(d)-scale] + "." + d[len(d)-scale:]
	out = strings.TrimRight(out, "0")
	return strings.TrimSuffix(out, ".")
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// mulSmall multiplies a digit string by a small positive integer.
func mulSmall(d string, k int) string {
	if k == 0 || d == "" {
		return "0"
	}
	out := make([]byte, 0, len(d)+4)
	carry := 0
	for i := len(d) - 1; i >= 0; i-- {
		p := int(d[i]-'0')*k + carry
		out = append(out, byte('0'+p%10))
		carry = p / 10
	}
	for carry > 0 {
		out = append(out, byte('0'+carry%10))
		carry /= 10
	}
	reverse(out)
	return strings.TrimLeft(string(out), "0")
}

// divSmall divides a digit string by a small positive integer that divides
// it exactly.
func divSmall(d string, k int) string {
	out := make([]byte, 0, len(d))
	rem := 0
	for i := 0; i < len(d); i++ {
		cur := rem*10 + int(d[i]-'0')
		out = append(out, byte('0'+cur/k))
		rem = cur % k
	}
	return strings.TrimLeft(string(out), "0")
}

// modSmall is a digit string modulo a small positive integer.
func modSmall(d string, k int) int {
	rem := 0
	for i := 0; i < len(d); i++ {
		rem = (rem*10 + int(d[i]-'0')) % k
	}
	return rem
}

func reverse(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}
