package api

import "fmt"

// InstanceDev and InstanceProd are the only two instance environments (039
// §3). They say which kind of worklode instance this process is — a dev
// instance that re-seeds and discards data, or a prod instance that holds the
// record. Unrelated to LODE_CLUSTER_ENV_MAP, which describes the environment
// of the deployments worklode *observes*.
const (
	InstanceDev  = "dev"
	InstanceProd = "prod"
)

// ParseInstanceEnv validates a LODE_INSTANCE_ENV value, returning the
// environment to run as.
//
// Empty means prod, because the failure modes are asymmetric (039 §3): a prod
// instance that silently believes it is dev drops decision records from the
// org's real data, while a dev instance that believes it is prod asks for a
// justification nobody needed. The permissive setting is the one an operator
// has to write down.
//
// Anything but dev or prod is an error rather than a fallback to either
// answer: an unrecognised value is a typo in a setting that changes what the
// server demands, so it fails startup.
func ParseInstanceEnv(s string) (string, error) {
	switch s {
	case "":
		return InstanceProd, nil
	case InstanceDev, InstanceProd:
		return s, nil
	default:
		return "", fmt.Errorf("LODE_INSTANCE_ENV: %q is not an instance environment, want %s or %s",
			s, InstanceDev, InstanceProd)
	}
}
