package bfd

import (
	"reflect"
	"testing"
)

func TestGreaterUInt32(t *testing.T) {
	type args struct {
		n []uint32
	}
	tests := []struct {
		name string
		args args
		want uint32
	}{
		{
			name: "1 arg",
			args: args{n: []uint32{5}},
			want: 5,
		},
		{
			name: "2 args",
			args: args{n: []uint32{5, 7}},
			want: 7,
		},
		{
			name: "2 args reverse",
			args: args{n: []uint32{7, 5}},
			want: 7,
		},
		{
			name: "3 args",
			args: args{n: []uint32{5, 7, 9}},
			want: 9,
		},
		{
			name: "3 args reverse",
			args: args{n: []uint32{9, 7, 5}},
			want: 9,
		},
		{
			name: "3 equal args",
			args: args{n: []uint32{9, 9, 9}},
			want: 9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GreaterUInt32(tt.args.n...); got != tt.want {
				t.Errorf("GreaterUInt32() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUniqueStringsSorted(t *testing.T) {
	type args struct {
		str []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "normal case",
			args: args{str: []string{"e", "f", "g", "f", "g", "c", "b", "g", "a", "c"}},
			want: []string{"a", "b", "c", "e", "f", "g"},
		},
		{
			name: "all unique",
			args: args{str: []string{"a", "b", "c", "d"}},
			want: []string{"a", "b", "c", "d"},
		},
		{
			name: "no elements",
			args: args{str: []string{}},
			want: []string{},
		},
		{
			name: "one element",
			args: args{str: []string{"a"}},
			want: []string{"a"},
		},
		{
			name: "all equal",
			args: args{str: []string{"a", "a", "a", "a", "a"}},
			want: []string{"a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UniqueStringsSorted(tt.args.str); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UniqueStringsSorted() = %v, want %v", got, tt.want)
			}
		})
	}
}
