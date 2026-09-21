package experience

import contract "github.com/Mujhtech/idenqa/contracts/experience/v1"

// SafeDefaultExperienceID is the stable identity of the Core-shipped accessible
// safe default. It is never tenant-owned and cannot be edited or revoked.
const SafeDefaultExperienceID = "exp_00000000000000000000000000"

// SafeDefaultLocale is the locale the safe default always carries.
const SafeDefaultLocale = "en"

// SafeDefaultDocument returns the Core-shipped accessible safe default. The
// document is signed at service construction and verified before use. It is
// deliberately unbranded, uses only safe contrast tokens, and references the
// Core-owned mandatory copy.
func SafeDefaultDocument() contract.Document {
	return contract.Document{
		SchemaVersion: contract.SchemaVersion,
		ExperienceID:  SafeDefaultExperienceID,
		Version:       1,
		Name:          "Idenqa safe default capture",
		Copy: contract.Copy{
			Version: "tc_idenqa_default_v1",
			Locales: []contract.LocaleCopy{
				{
					Locale: SafeDefaultLocale,
					Entries: []contract.CopyEntry{
						{Key: "capture.title", Value: "Verify your identity"},
						{Key: "capture.instruction", Value: "Follow the steps on screen. Take your time."},
						{Key: "capture.review", Value: "Check your capture before you continue."},
						{Key: "capture.retry", Value: "You can try again if the image is not clear."},
						{Key: "capture.processing", Value: "Your capture is being checked."},
						{Key: "capture.complete", Value: "You have finished this step."},
					},
				},
			},
		},
		MandatoryCopyVersion: DefaultMandatoryVersion,
		DefaultLocale:        SafeDefaultLocale,
		Targeting:            nil,
		Links: contract.Links{
			Support: "https://idenqa.dev/support",
			Privacy: "https://idenqa.dev/privacy",
			Terms:   "https://idenqa.dev/terms",
		},
		Theme: contract.Theme{
			PrimaryColor:    "#1f6feb",
			AccentColor:     "#0b3d91",
			BackgroundColor: "#ffffff",
			TextColor:       "#1b1f23",
		},
	}
}
