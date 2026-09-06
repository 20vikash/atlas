package vm

import "testing"

func TestUserIDRangeAllocatesLowestUnusedID(t *testing.T) {
	userIDRange := UserIDRange{Min: 100, Max: 102}

	tests := []struct {
		name        string
		usedUserIDs map[uint32]bool
		want        uint32
		wantError   bool
	}{
		{"empty", nil, 100, false},
		{"skips used", map[uint32]bool{100: true, 101: true}, 102, false},
		{"lowest free in gap", map[uint32]bool{101: true}, 100, false},
		{"ignores out-of-range", map[uint32]bool{50: true, 100: true, 200: true}, 101, false},
		{"exhausted", map[uint32]bool{100: true, 101: true, 102: true}, 0, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			userID, err := userIDRange.allocate(test.usedUserIDs)
			if test.wantError {
				if err == nil {
					t.Fatalf("want error, got user ID %d", userID)
				}
				return
			}

			if err != nil {
				t.Fatalf("allocate user ID: %v", err)
			}
			if userID != test.want {
				t.Fatalf("user ID = %d, want %d", userID, test.want)
			}
		})
	}
}
