// Package egress builds the outbound HTTP client every service uses to reach
// the public internet. Outbound traffic leaves the cluster through the egress
// service's proxy, selected by an explicit EGRESS_PROXY_URL; HTTP_PROXY,
// HTTPS_PROXY, ALL_PROXY and NO_PROXY are never read.
//
// An empty EGRESS_PROXY_URL is an error, not a silent direct call. The only
// escape is EGRESS_DIRECT=true together with APP_ENV=local.
package egress
