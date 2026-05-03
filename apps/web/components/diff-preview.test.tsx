import React from "react";
import { render, screen, within } from "@testing-library/react";

import { DiffPreview } from "./diff-preview";

describe("DiffPreview", () => {
  it("renders every diff section with original, revised, reason, and citation ids", () => {
    const sections = [
      {
        citation_ids: ["cit-001", "cit-002"],
        original: "The policy may be updated in future versions.",
        reason: "Clarify the approval owner and review cadence.",
        revised: "The policy is reviewed by HR every quarter before publication.",
        section_title: "Policy Updates"
      },
      {
        citation_ids: ["cit-003"],
        original: "Managers should handle the process as needed.",
        reason: "Replace vague guidance with an explicit escalation path.",
        revised: "Managers must escalate unresolved cases to compliance within 24 hours.",
        section_title: "Escalation"
      }
    ];

    render(<DiffPreview sections={sections} />);

    const cards = screen.getAllByTestId("diff-section");

    expect(cards).toHaveLength(2);

    sections.forEach((section, index) => {
      const card = cards[index];

      expect(within(card).getByText(section.section_title)).toBeInTheDocument();
      expect(within(card).getByText(section.original)).toBeInTheDocument();
      expect(within(card).getByText(section.revised)).toBeInTheDocument();
      expect(within(card).getByText(section.reason)).toBeInTheDocument();

      section.citation_ids.forEach((citationId) => {
        expect(within(card).getByText(citationId)).toBeInTheDocument();
      });
    });
  });

  it("renders explicit occurrence labels for duplicate titles", () => {
    const consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const sections = [
      {
        citation_ids: ["cite_1"],
        original: "old-1",
        reason: "clarify-1",
        revised: "new-1",
        section_occurrence: 1,
        section_title: "Policy Updates"
      },
      {
        citation_ids: ["cite_1"],
        original: "old-2",
        reason: "clarify-2",
        revised: "new-2",
        section_occurrence: 2,
        section_title: "Policy Updates"
      }
    ];

    render(<DiffPreview sections={sections} />);

    expect(screen.getByText("Policy Updates · 第 1 处")).toBeInTheDocument();
    expect(screen.getByText("Policy Updates · 第 2 处")).toBeInTheDocument();
    expect(consoleErrorSpy).not.toHaveBeenCalledWith(
      expect.stringContaining("Encountered two children with the same key")
    );

    consoleErrorSpy.mockRestore();
  });

  it("falls back to local occurrence labels for legacy duplicate titles", () => {
    const consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const sections = [
      {
        citation_ids: ["cite_1"],
        original: "old-1",
        reason: "clarify-1",
        revised: "new-1",
        section_title: "Policy Updates"
      },
      {
        citation_ids: ["cite_1"],
        original: "old-2",
        reason: "clarify-2",
        revised: "new-2",
        section_title: "Policy Updates"
      }
    ];

    render(<DiffPreview sections={sections} />);

    expect(screen.getByText("Policy Updates · 第 1 处")).toBeInTheDocument();
    expect(screen.getByText("Policy Updates · 第 2 处")).toBeInTheDocument();
    expect(consoleErrorSpy).not.toHaveBeenCalledWith(
      expect.stringContaining("Encountered two children with the same key")
    );

    consoleErrorSpy.mockRestore();
  });
});
