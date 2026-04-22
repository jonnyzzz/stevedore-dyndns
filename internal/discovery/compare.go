package discovery

import "fmt"

// ServicesEqual compares two service lists based on fields that affect routing.
func ServicesEqual(a, b []Service) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}

	counts := make(map[string]int, len(a))
	for _, svc := range a {
		counts[serviceKey(svc)]++
	}
	for _, svc := range b {
		key := serviceKey(svc)
		if counts[key] == 0 {
			return false
		}
		counts[key]--
	}
	for _, remaining := range counts {
		if remaining != 0 {
			return false
		}
	}
	return true
}

func serviceKey(svc Service) string {
	return fmt.Sprintf("%s|%d|%t|%s", svc.Subdomain, svc.Port, svc.Websocket, svc.GetHealthPath())
}
