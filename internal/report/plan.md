# Report Module Plan

## 1. Objective

The report module is responsible for generating and saving detailed bug reports when a vulnerability or crash is discovered by the fuzzer. The reports should be clear, informative, and provide enough information for a developer to understand and reproduce the bug.

## 2. Data Structures

The primary data structure for this module will be the `Bug` struct, which will be defined in the `analysis` package. The report module will consume this struct to generate a report.

## 3. Functionality

### 3.1. Report Generation

-   **`Generate(bug *analysis.Bug) (string, error)`**: This function will take a `Bug` object and generate a human-readable report in Markdown format. The report should include:
    -   A title summarizing the bug (e.g., "Crash in function `XYZ`").
    -   The input that caused the bug.
    -   The stack trace or error message.
    -   Any other relevant information from the `Bug` object.

### 3.2. Report Saving

-   **`Save(reportContent string, outputPath string) error`**: This function will save the generated report content to a specified file path. The filename should be unique, perhaps using a timestamp or a hash of the bug details.

## 4. Implementation Details

-   The `Reporter` interface will be implemented by a struct, e.g., `FileReporter`.
-   The `Save` method of `FileReporter` will first call `Generate` to create the report content and then save it to a file.

## 5. Testing

-   **`TestGenerate`**: A unit test to verify that the `Generate` function produces a correctly formatted Markdown report for a sample `Bug` object.
-   **`TestSave`**: A unit test to verify that the `Save` function correctly writes a report to the filesystem. This test should create a temporary file and clean it up afterward.
