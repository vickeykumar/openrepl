
// other App
(function otherApp() {
    $(document).ready(function() {
      // update the modal topics
      const $topicSelect = $("#topic");
      topics.forEach(topic => {
          $topicSelect.append(`<option value="${topic}">${topic}</option>`);
      });

      // Initialize DataTable. Column 4 (Last Added) is treated as number.
      let table = $('#questionsTable').DataTable({
        "scrollX": true, // Enables horizontal scrolling
        "columnDefs": [
          { targets: [0, 5], orderable: false },
        ],
        order: [[4, 'desc']], // Default sorting: Most recent questions first
        "responsive": true, // Enable responsive behavior
        "autoWidth": false, // Prevent automatic width expansion
        "language": {
            "emptyTable": "No coding questions available. Please click on 'New Question' to add new questions."
        }
      });

      // Retrieve stored data from localStorage.
      let bookmarkedRows = JSON.parse(localStorage.getItem('bookmarkedRows')) || [];
      let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];

      // Update bookmark checkboxes and (in default mode) reorder rows so that pinned rows come first.
      function updateBookmarks() {
        let currentSort = $('#sortBy').val();
        let nonBookmarkedRows = [];
        let bookmarkedRowsList = [];

        $('#questionsTable tbody tr').each(function() {
            let rowId = $(this).attr('data-id');
            let checkbox = $(this).find('.bookmark');
            
            if (bookmarkedRows.includes(rowId)) {
                checkbox.prop('checked', true);
                $(this).addClass('bookmarked-row');
                bookmarkedRowsList.push($(this)); // Collect bookmarked rows
            } else {
                checkbox.prop('checked', false);
                $(this).removeClass('bookmarked-row');
                nonBookmarkedRows.push($(this)); // Collect non-bookmarked rows
            }
        });

        // Sort non-bookmarked rows in descending order of `data-added`
        nonBookmarkedRows.sort((a, b) => {
            return parseInt($(b).attr('data-added')) - parseInt($(a).attr('data-added'));
        });

        // Append sorted non-bookmarked rows first
        nonBookmarkedRows.forEach(row => $('#questionsTable tbody').append(row));

        // Append bookmarked rows to the bottom
        bookmarkedRowsList.forEach(row => $('#questionsTable tbody').append(row));
      }


      // Load stored questions and add them to the table.
      function loadStoredQuestions() {
        storedQuestions.forEach(q => addQuestionRow(q));
      }

      // Add a question row to the DataTable.
      function addQuestionRow(q) {
        // The "Last Added" cell displays a human-readable date/time (using toLocaleString)
        // and uses a data-order attribute (with the epoch timestamp) for sorting.
        let newRow = `<tr data-id="${q.id}" data-difficulty="${q.difficulty}" data-added="${q.added}">
          <td><input type="checkbox" class="bookmark"></td>
          <td><a href="/practice?name=${q.nameHyphenated}" class="question-link" target="_blank">${q.name}</a></td>
          <td>${q.topic}</td>
          <td>${q.difficulty}</td>
          <td data-order="${q.added}">${new Date(q.added).toLocaleString()}</td>
          <td><button class="delete-btn">🗑 Delete</button></td>
        </tr>`;
        table.row.add($(newRow)).draw(false);
        // Move newly added rows to the top
        let lastIndex = table.rows().count() - 1;
        let newRowNode = table.row(lastIndex).node();
        $(newRowNode).prependTo('#questionsTable tbody');

        updateBookmarks();

        // Ensure the topic is added to the filter dropdown if it's new
        if ($("#topicsFilter option[value='" + q.topic + "']").length === 0) {
          $("#topicsFilter").append(`<option value="${q.topic}">${q.topic}</option>`);
        }

        // Ensure the difficulty level is added to the filter dropdown if it's new
        if ($("#difficultyFilter option[value='" + q.difficulty + "']").length === 0) {
          $("#difficultyFilter").append(`<option value="${q.difficulty}">${q.difficulty}</option>`);
        }
      }

      // Handle bookmark checkbox changes.
      $('#questionsTable tbody').on('change', '.bookmark', function() {
        let row = $(this).closest('tr');
        let rowId = row.attr('data-id');
        if ($(this).prop('checked')) {
          bookmarkedRows.push(rowId);
        } else {
          bookmarkedRows = bookmarkedRows.filter(id => id !== rowId);
        }
        localStorage.setItem('bookmarkedRows', JSON.stringify(bookmarkedRows));
        updateBookmarks();
      });

      // Handle row deletion.
      $('#questionsTable tbody').on('click', '.delete-btn', function() {
        let row = $(this).closest('tr');
        let rowId = row.attr('data-id');
        storedQuestions = storedQuestions.filter(q => q.id !== rowId);
        localStorage.setItem('questions', JSON.stringify(storedQuestions));
        bookmarkedRows = bookmarkedRows.filter(id => id !== rowId);
        localStorage.setItem('bookmarkedRows', JSON.stringify(bookmarkedRows));
        table.row(row).remove().draw();
        updateBookmarks();
      });

      // Handle sort/filter changes.
      $('#sortBy').on('change', function() {
        let selectedSort = $(this).val();
        if (selectedSort === "recent") {
          // Sort by the epoch timestamp (descending).
          table.order([4, 'desc']).draw();
        } else if (selectedSort === "") {
          // Default case: clear filters and then move bookmarked rows to the top.
          table.search('').columns().search('').draw();
          updateBookmarks();
        } else {
          // Filter by difficulty level.
          table.column(3).search(selectedSort).draw();
        }
      });

      // Handle random question button.
      $('#randomQuestionBtn').click(function() {
        if (storedQuestions.length === 0) {
          alert("No questions available!");
          return;
        }
        let randomIndex = Math.floor(Math.random() * storedQuestions.length);
        let randomQuestion = storedQuestions[randomIndex];
        window.open(`/practice?name=${randomQuestion.nameHyphenated}`, "_blank");
      });

      // Initialize by loading stored questions.
      loadStoredQuestions();
      updateBookmarks();
      $("#topic").select2({
          placeholder: "Search or Select a Topic",
          allowClear: true
      });

      // Modal
      const open = document.getElementById("newQuestionBtn");
      const close = document.getElementById("close");
      const modal = document.getElementById("modal");
      const button = document.getElementById("qsubmit-btn");
      open.addEventListener("click", () => modal.classList.add("show-modal"));
      button.addEventListener("click", function(event) {
        event.preventDefault(); // Prevent form submission
        $("#temperature").val(globaltemperature);
        
        // Get values from form inputs
        let topic = document.getElementById("topic").value.trim();
        let difficultyLevel = document.getElementById("difficulty").value.trim();
        let customPrompt = document.getElementById("customPrompt").value.trim();
        let tempValue = parseFloat($("#temperature").val());
        if (!isNaN(tempValue)) {
          globaltemperature = tempValue;
          localStorage.setItem("temperature", tempValue); // Save to localStorage
        }

        // Call the function and handle the Promise
        generateNewQuestion(topic, difficultyLevel, customPrompt)
            .then(newQuestion => {
                if (!newQuestion) {
                    return;
                }

                // Store and display the new question
                let result = saveNewQuestions(newQuestion);

                if (result.error === null) {
                    addQuestionRow(newQuestion);
                    console.log("New question added:", newQuestion);
                    storedQuestions = result.storedQuestions;
                } else {
                    alert("Error occurred while saving new question: " + result.error);
                }
                modal.classList.remove("show-modal");
            })
            .catch(error => {
                console.error("Error generating a new question:", error);
                alert("An unexpected error occurred while generating a new question.");
            });
      });

      close.addEventListener("click", () => modal.classList.remove("show-modal"));
      window.addEventListener("click", (e) =>
        e.target == modal ? modal.classList.remove("show-modal") : false
      );

      $('.filter-select').select2(); // Enhance dropdowns with search

      // Filtering logic
      $(".filter-select").on("change", function () {
          table.draw(); // Refresh table when dropdowns change
      });

      // Custom filtering for DataTables
      $.fn.dataTable.ext.search.push(function (settings, rowData) {
          let topicsFilter = $('#topicsFilter').val().toLowerCase();
          let difficultyFilter = $('#difficultyFilter').val().toLowerCase();

          let topics = rowData[2].toLowerCase();
          let difficulty = rowData[3].toLowerCase();

          return (
              (topicsFilter === "" || topics.includes(topicsFilter)) &&
              (difficultyFilter === "" || difficulty.includes(difficultyFilter))
          );
      });
    });
})();