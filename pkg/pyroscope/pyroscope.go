package pyroscope

import (
	"os"
	"runtime"

	"github.com/grafana/pyroscope-go"
)

type PyroConfig interface {
	GetServerAddress() string
	GetServerUsername() string
	GetServerPassword() string
}

func InitPyroscope(namespace string, appName string, config PyroConfig) error {
	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(5)

	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: appName,
		// replace this with the address of pyroscope server
		ServerAddress: config.GetServerAddress(),
		// set basic auth username and password if needed
		BasicAuthUser:     config.GetServerUsername(),
		BasicAuthPassword: config.GetServerPassword(),
		// you can provide static tags via a map:
		Tags: map[string]string{
			"host":      os.Getenv("HOSTNAME"),
			"namespace": namespace,
		},

		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
	return err
}
