package money

import (
	"fmt"
	"math/big"
)

var scaleFactors = [...]int64{1, 10, 100, 1000, 10000}

// Rat возвращает точное рациональное представление суммы в виде копии *big.Rat.
func (a Amount) Rat() *big.Rat {
	return new(big.Rat).SetFrac(new(big.Int).Set(a.coefficient()), big.NewInt(scaleFactors[Scale]))
}

// FromRat преобразует рациональное число *big.Rat в Amount, округляя до places знаков (0..4)
// по правилу половины от нуля (Half Away From Zero).
func FromRat(r *big.Rat, places int) (Amount, error) {
	if r == nil {
		return Amount{}, fmt.Errorf("%w: nil rational value", ErrInvalidAmount)
	}
	if places < 0 || places > Scale {
		return Amount{}, ErrInvalidScale
	}
	if r.Sign() == 0 {
		return Amount{}, nil
	}

	factor := big.NewInt(scaleFactors[places])
	scaledNum := new(big.Int).Mul(r.Num(), factor)
	denom := r.Denom()

	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(scaledNum, denom, remainder)

	twiceRem := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
	if twiceRem.Cmp(denom) >= 0 {
		quotient.Add(quotient, big.NewInt(int64(r.Sign())))
	}

	step := big.NewInt(scaleFactors[Scale-places])
	return Amount{units: new(big.Int).Mul(quotient, step)}, nil
}
