package main

import "fmt"

// checkVocabFlags rejects vocabulary flags that the chosen registry source
// would accept and then ignore. An ignored vocabulary flag leaves the operator
// believing validation is on when it is not.
func checkVocabFlags(source, vocabFile, gitVocab string) error {
	switch source {
	case "git":
		if vocabFile != "" {
			return fmt.Errorf("--vocab-file is not used with --registry-source=git; use --registry-git-vocab (path within the repo)")
		}
	case "file":
		if gitVocab != "" {
			return fmt.Errorf("--registry-git-vocab is only valid with --registry-source=git; use --vocab-file")
		}
	}
	return nil
}

// fileModeOpen reports whether a file-source daemon would start without
// vocabulary validation. Git mode reports its own effective mode from the
// store on every load, so it is never warned about here.
func fileModeOpen(source, vocabFile, vocabularyExt string) bool {
	return source == "file" && vocabFile == "" && vocabularyExt == ""
}
