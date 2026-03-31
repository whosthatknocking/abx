package sqlite

import (
	"strconv"
	"time"
)

type typesTime = time.Time

func parseStdTime(v string) (time.Time, error) {
	return time.Parse(timeLayout, v)
}

func randomSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
