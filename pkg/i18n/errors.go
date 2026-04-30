package i18n

import "errors"

// ErrKeyNotFound is returned when a key is missing in every layer
// of the bundle (target locale, default locale, and any layered fallback).
var ErrKeyNotFound = errors.New("i18n: key not found")

// ErrTranslationFailed is returned when the underlying engine
// reports a translation/template failure for a key that does exist.
var ErrTranslationFailed = errors.New("i18n: translation failed")
