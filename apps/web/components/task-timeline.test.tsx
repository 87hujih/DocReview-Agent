import React from "react";
import { render, screen } from "@testing-library/react";

import { TaskTimeline } from "./task-timeline";

describe("TaskTimeline", () => {
  it("renders task event messages together with step summaries", () => {
    render(
      <TaskTimeline
        events={[
          {
            created_at: "2026-04-12T10:00:00+08:00",
            event_type: "artifact.created",
            id: "event-1",
            level: "info",
            message: "引用产物已生成",
            payload: {
              artifact_type: "citations"
            },
            run_id: null,
            source: "orchestrator",
            step_name: "retriever",
            task_id: "task-1"
          }
        ]}
        steps={[
          {
            completed_at: "2026-04-12T10:00:03+08:00",
            id: "step-1",
            started_at: "2026-04-12T10:00:00+08:00",
            status: "completed",
            step_name: "retriever"
          }
        ]}
      />
    );

    expect(screen.getAllByText("检索器")).toHaveLength(2);
    expect(screen.getByText("事件流")).toBeInTheDocument();
    expect(screen.getByText("artifact.created")).toBeInTheDocument();
    expect(screen.getByText("引用产物已生成")).toBeInTheDocument();
  });
});
