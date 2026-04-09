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
});
