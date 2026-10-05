package events

import "time"

// DefaultReaderMaxWait is the fleet's fetch long-poll for every Kafka consumer:
// set it as kafka.ReaderConfig.MaxWait on every consumer reader a service builds.
//
// Why: a kafka-go consumer-group Reader.Close waits out each partition's
// in-flight Fetch, a broker long-poll of up to MaxWait whose socket read
// deadline is that same MaxWait (kafka-go's default is 10 s). With every reader
// in one lifecycle CloserGroup, the shutdown cost of closing them is therefore
// one MaxWait — 2 s instead of 10 s of the 25 s shutdown budget. Delivery
// latency is unaffected: a fetch returns as soon as data arrives, MaxWait only
// bounds how long the broker holds an EMPTY fetch.
//
// Trade-off (accepted by owner decision 2026-10-05): an idle consumer sends a
// fetch request every 2 s instead of every 10 s.
const DefaultReaderMaxWait = 2 * time.Second
