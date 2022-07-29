package set

type Set[T comparable] map[T]struct{}

func NewSet[T comparable]() Set[T] {
	return make(Set[T])
}

func (s Set[comparable]) Add(v comparable) {
	s[v] = struct{}{}
}

func (s Set[comparable]) AddOnce(v comparable, condition *bool) {
	if *condition {
		return
	}

	s[v] = struct{}{}

	*condition = true
}

func (s Set[comparable]) Remove(v comparable) {
	delete(s, v)
}

func (s Set[comparable]) Has(v comparable) bool {
	_, ok := s[v]
	return ok
}

func (s Set[comparable]) Clear() {
	for v := range s {
		delete(s, v)
	}
}

func (s Set[comparable]) GetAll() []comparable {
	var keys []comparable

	for k := range s {
		keys = append(keys, k)
	}

	return keys
}
