// Пакет money реализует точные денежные значения без float.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const Scale = 4

var (
	ErrInvalidAmount = errors.New("invalid monetary amount")
	ErrInvalidScale  = errors.New("rounding scale must be between 0 and 4")
)

// Amount хранит сумму с четырьмя дробными знаками; нулевое значение равно нулю.
// Копии могут разделять коэффициент: операции создают новый big.Int и не меняют исходный.
type Amount struct {
	units *big.Int
}

// Parse допускает ведущие нули и отрицательный ноль: String вернёт "1.2000" для "001.2" и "0.0000" для "-0.0000".
// Лишние дробные знаки отклоняются, а не округляются молча.
func Parse(value string) (Amount, error) {
	original := value
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = value[1:]
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || !digits(parts[0]) {
		return Amount{}, fmt.Errorf("%w: expected decimal digits", ErrInvalidAmount)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if !digits(fraction) || len(fraction) > Scale {
			return Amount{}, fmt.Errorf("%w: expected 1 to %d fractional digits", ErrInvalidAmount, Scale)
		}
	}
	coefficient := parts[0] + fraction + strings.Repeat("0", Scale-len(fraction))
	units, ok := new(big.Int).SetString(coefficient, 10)
	if !ok {
		return Amount{}, fmt.Errorf("%w: cannot parse %q", ErrInvalidAmount, original)
	}
	if negative {
		units.Neg(units)
	}
	return Amount{units: units}, nil
}

func digits(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (a Amount) coefficient() *big.Int {
	if a.units == nil {
		return new(big.Int)
	}
	return a.units
}

func (a Amount) String() string {
	abs := new(big.Int).Abs(a.coefficient()).String()
	if len(abs) <= Scale {
		abs = strings.Repeat("0", Scale+1-len(abs)) + abs
	}
	point := len(abs) - Scale
	result := abs[:point] + "." + abs[point:]
	if a.Sign() < 0 {
		result = "-" + result
	}
	return result
}

func (a Amount) Sign() int { return a.coefficient().Sign() }

func (a Amount) Cmp(b Amount) int { return a.coefficient().Cmp(b.coefficient()) }

func (a Amount) Add(b Amount) Amount {
	return Amount{units: new(big.Int).Add(a.coefficient(), b.coefficient())}
}

func (a Amount) Sub(b Amount) Amount {
	return Amount{units: new(big.Int).Sub(a.coefficient(), b.coefficient())}
}

func (a Amount) Negate() Amount {
	return Amount{units: new(big.Int).Neg(a.coefficient())}
}

// Round округляет половины от нуля: 1.2350 до двух знаков даёт 1.2400, -1.2350 — -1.2400.
func (a Amount) Round(places int) (Amount, error) {
	if places < 0 || places > Scale {
		return Amount{}, ErrInvalidScale
	}
	factors := [...]int64{1, 10, 100, 1000, 10000}
	divisor := big.NewInt(factors[Scale-places])
	remainder := new(big.Int)
	quotient := new(big.Int)
	quotient.QuoRem(a.coefficient(), divisor, remainder)
	twiceRemainder := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
	if twiceRemainder.Cmp(divisor) >= 0 {
		quotient.Add(quotient, big.NewInt(int64(a.Sign())))
	}
	return Amount{units: new(big.Int).Mul(quotient, divisor)}, nil
}

// Сумма передаётся строкой, чтобы JSON-клиент не потерял точность.
func (a Amount) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }

// При ошибке разбора исходная сумма остаётся неизменной.
func (a *Amount) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("%w: JSON amount must be a decimal string", ErrInvalidAmount)
	}
	parsed, err := Parse(value)
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}
