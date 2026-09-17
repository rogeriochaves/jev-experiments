package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Test struct {
	Args   []any `json:"args"`
	Expect any   `json:"expect"`
}

type Task struct {
	Name        string
	Description string
	Params      []scopeVar
	Tests       []Test
}

func (t Task) signature() string {
	var names []string
	for _, p := range t.Params {
		names = append(names, p.name)
	}
	return fmt.Sprintf("function %s(%s)", t.Name, strings.Join(names, ", "))
}

func (t Task) callString(test Test) string {
	var args []string
	for _, a := range test.Args {
		b, _ := json.Marshal(a)
		args = append(args, string(b))
	}
	return fmt.Sprintf("%s(%s)", t.Name, strings.Join(args, ", "))
}

func num(name string) scopeVar { return scopeVar{name: name, kind: "number"} }
func str(name string) scopeVar { return scopeVar{name: name, kind: "string"} }
func arr(name string) scopeVar { return scopeVar{name: name, kind: "array"} }

var tasks = []Task{
	{"add", "Return the sum of the two numbers.", []scopeVar{num("rock"), num("fire")},
		[]Test{{[]any{1, 2}, 3}, {[]any{10, -3}, 7}, {[]any{0, 0}, 0}}},
	{"bigger", "Return the larger of the two numbers.", []scopeVar{num("rock"), num("fire")},
		[]Test{{[]any{1, 2}, 2}, {[]any{10, -3}, 10}, {[]any{5, 5}, 5}}},
	{"isEven", "Return true when the number is even, false otherwise.", []scopeVar{num("rock")},
		[]Test{{[]any{2}, true}, {[]any{7}, false}, {[]any{0}, true}}},
	{"absolute", "Return the number without its sign.", []scopeVar{num("rock")},
		[]Test{{[]any{-5}, 5}, {[]any{3}, 3}, {[]any{0}, 0}}},
	{"shout", "Return the word in capital letters.", []scopeVar{str("word")},
		[]Test{{[]any{"hello"}, "HELLO"}, {[]any{"Rock"}, "ROCK"}, {[]any{""}, ""}}},
	{"howLong", "Return how many characters the word has.", []scopeVar{str("word")},
		[]Test{{[]any{"hello"}, 5}, {[]any{""}, 0}, {[]any{"ab"}, 2}}},
	{"first", "Return the first item of the array.", []scopeVar{arr("tribe")},
		[]Test{{[]any{[]any{4, 5, 6}}, 4}, {[]any{[]any{"a"}}, "a"}, {[]any{[]any{9, 1}}, 9}}},
	{"last", "Return the last item of the array.", []scopeVar{arr("tribe")},
		[]Test{{[]any{[]any{4, 5, 6}}, 6}, {[]any{[]any{"a"}}, "a"}, {[]any{[]any{9, 1}}, 1}}},
	{"total", "Return the sum of all numbers in the array.", []scopeVar{arr("tribe")},
		[]Test{{[]any{[]any{1, 2, 3}}, 6}, {[]any{[]any{}}, 0}, {[]any{[]any{10, -4}}, 6}}},
	{"countBig", "Return how many numbers in the array are greater than 10.", []scopeVar{arr("tribe")},
		[]Test{{[]any{[]any{1, 20, 30}}, 2}, {[]any{[]any{}}, 0}, {[]any{[]any{10, 11}}, 1}}},
	{"has", "Return true when the array contains the number, false otherwise.", []scopeVar{arr("tribe"), num("rock")},
		[]Test{{[]any{[]any{1, 2, 3}, 2}, true}, {[]any{[]any{1, 2, 3}, 5}, false}, {[]any{[]any{}, 1}, false}}},
	{"factorial", "Return the factorial of the number: the product of all whole numbers from 1 up to the number. The factorial of 0 is 1.", []scopeVar{num("rock")},
		[]Test{{[]any{0}, 1}, {[]any{3}, 6}, {[]any{5}, 120}}},
	{"clamp", "Return the number limited to the range: not below the low bound and not above the high bound.", []scopeVar{num("rock"), num("low"), num("high")},
		[]Test{{[]any{5, 0, 10}, 5}, {[]any{-3, 0, 10}, 0}, {[]any{50, 0, 10}, 10}}},
	{"sign", "Return 1 when the number is positive, -1 when negative, 0 when zero.", []scopeVar{num("rock")},
		[]Test{{[]any{5}, 1}, {[]any{-3}, -1}, {[]any{0}, 0}}},
	{"reverseWord", "Return the word with its characters in reverse order.", []scopeVar{str("word")},
		[]Test{{[]any{"abc"}, "cba"}, {[]any{""}, ""}, {[]any{"rock"}, "kcor"}}},
	{"biggest", "Return the largest number in the array. The array is never empty.", []scopeVar{arr("tribe")},
		[]Test{{[]any{[]any{1, 9, 3}}, 9}, {[]any{[]any{-5, -2}}, -2}, {[]any{[]any{7}}, 7}}},
}
