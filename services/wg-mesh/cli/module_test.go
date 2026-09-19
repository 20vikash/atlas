package main

import "testing"

func TestKernelModuleReleaseReadsTheFirstVermagicField(t *testing.T) {
	cases := map[string]string{
		"6.8.0-138-generic SMP preempt_dynamic modules_unload modversions": "6.8.0-138-generic",
		"  6.8.0-138-generic  SMP  ":                                       "6.8.0-138-generic",
		"":                                                                 "",
		"   ":                                                              "",
	}

	for vermagic, want := range cases {
		if got := kernelModuleRelease(vermagic); got != want {
			t.Errorf("kernelModuleRelease(%q) = %q, want %q", vermagic, got, want)
		}
	}
}
