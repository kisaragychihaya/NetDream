package ndapi

// Pair is one key/value pair of a request or a response, as a script sees it.
type Pair struct {
	Key   string
	Value string
}

// Request is what a pre script sees in nd.request. The caller fills it from the
// request it is about to send; the script's edits are read back from the object
// RequestGlobal builds.
type Request struct {
	Method  string
	URL     string
	Headers []Pair
	Query   []Pair
	// Body is the text nd.request.body starts as.
	Body string
}

// Response is what a post script sees in nd.response.
type Response struct {
	Status     int
	StatusText string
	Headers    []Pair
	Body       string
	TimeMS     int64
	SizeBytes  int
}
