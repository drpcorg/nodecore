package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// parseSize converts a size string - a positive integer followed by KB or MB -
// to bytes. It is the one size format of the config: cache policies
// (object-max-size) and server limits (max-request-size) both use it.
func parseSize(size string) (int64, error) {
	var maxSize string
	var multiplier int64

	if strings.HasSuffix(size, "MB") {
		maxSize = strings.TrimSuffix(size, "MB")
		multiplier = 1024 * 1024
	} else if strings.HasSuffix(size, "KB") {
		maxSize = strings.TrimSuffix(size, "KB")
		multiplier = 1024
	} else {
		return 0, errors.New("size must be in KB or MB")
	}

	maxSizeInt, err := strconv.ParseInt(strings.TrimSpace(maxSize), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("couldn't parse size - %s", err.Error())
	}
	if maxSizeInt <= 0 {
		return 0, errors.New("size must be > 0")
	}
	if maxSizeInt > math.MaxInt64/multiplier {
		return 0, errors.New("size is too large")
	}

	return maxSizeInt * multiplier, nil
}
