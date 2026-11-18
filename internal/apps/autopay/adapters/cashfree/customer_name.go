package cashfree

import "hash/fnv"

// Cashfree validates customer_name as a "person name" and rejects the whole request (customer_name_invalid) for
// digits, symbols or non-Latin scripts, which real user names often contain (e.g. "Kumari 24×7" or a Devanagari
// channel name). The name is cosmetic for us, so instead of forwarding users.name we send a Latin placeholder.
var (
	placeholderFirstNames = []string{
		"Aarav", "Vivaan", "Aditya", "Arjun", "Rohan", "Rahul", "Amit", "Suresh", "Ramesh", "Vijay", "Anil", "Sanjay",
		"Priya", "Anjali", "Neha", "Pooja", "Sunita", "Kavita", "Meena", "Rekha", "Anita", "Sneha", "Divya", "Riya",
	}
	placeholderLastNames = []string{
		"Sharma", "Verma", "Gupta", "Singh", "Patel", "Kumar", "Yadav", "Joshi",
		"Mehta", "Reddy", "Nair", "Iyer", "Das", "Mishra", "Pandey", "Chauhan",
	}
)

// customerName returns the name sent to Cashfree for a user. It is picked from a fixed list but keyed on the user
// id, so one user always gets the same name (stable across retries, mandates and one-time payments).
func customerName(userID string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID))
	sum := h.Sum32()
	first := placeholderFirstNames[sum%uint32(len(placeholderFirstNames))]
	last := placeholderLastNames[(sum/uint32(len(placeholderFirstNames)))%uint32(len(placeholderLastNames))]
	return first + " " + last
}
