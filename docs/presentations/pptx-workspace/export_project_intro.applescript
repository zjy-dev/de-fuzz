-- Native PowerPoint export; absolute POSIX paths are supplied as arguments.
-- Example: osascript export_project_intro.applescript "$PWD/项目介绍-DeFuzz.pptx" "$PWD/项目介绍-DeFuzz.pdf"
on run argv
	if (count of argv) is not 2 then error "Provide input.pptx and output.pdf absolute paths."
	set inputPath to (POSIX file (item 1 of argv)) as alias
	set outputPath to (POSIX file (item 2 of argv)) as text
	tell application "Microsoft PowerPoint"
		open inputPath
		set deck to active presentation
		save deck in outputPath as save as PDF
		close deck saving no
	end tell
end run
