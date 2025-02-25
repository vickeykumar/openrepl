
// other App
(function otherApp() {
    $(document).ready(function() {
      // update the modal topics
      const $topicSelect = $("#topic");
      topics.forEach(topic => {
          $topicSelect.append(`<option value="${topic}">${topic}</option>`);
      });

      // Initialize DataTable. Column 5 (Last updated) is treated as number.
      let table = $('#questionsTable').DataTable({
        "scrollX": true, // Enables horizontal scrolling
        "columnDefs": [
          { targets: [0, 4, 6], orderable: false },
        ],
        "responsive": true, // Enable responsive behavior
        "autoWidth": false, // Prevent automatic width expansion
        "language": {
            "emptyTable": "No coding questions available. Please click on 'New Question' to add new questions."
        }
      });

      // Update bookmark checkboxes and (in default mode) reorder rows so that pinned rows come first.
      function updateBookmarks() {
        let nonBookmarkedRows = [];
        let bookmarkedRowsList = [];

        $('#questionsTable tbody tr').each(function() {
            let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
            let rowId = $(this).attr('data-id');
            let checkbox = $(this).find('.bookmark');
            let question = storedQuestions.find(q => q.id === rowId);

            if (question?.bookmarkStatus) {
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
            const addedA = parseInt($(a).attr('data-added'));
            const addedB = parseInt($(b).attr('data-added'));

            if (addedA !== addedB) {
                return addedB - addedA; // Sort by 'data-added' DESC
            }

            return parseInt($(a).attr('data-id')) - parseInt($(b).attr('data-id')); // Sort by 'index' ASC
        });

        // Append sorted non-bookmarked rows first
        nonBookmarkedRows.forEach(row => $('#questionsTable tbody').append(row));

        // Append bookmarked rows to the bottom
        bookmarkedRowsList.forEach(row => $('#questionsTable tbody').append(row));
      }

      async function fetchAndStoreQuestions() {
        try {
          const response = await fetch('/js/dsa.json');
          const questions = await response.json();
          let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
          // Map existing questions by nameHyphenated for quick lookup
          const storedMap = new Map(storedQuestions.map(q => [q.nameHyphenated, q]));

          const newQuestions = questions
            .filter(({ title }) => {
              const nameHyphenated = title.replace(/\s+/g, '-').toLowerCase();
              return !storedMap.has(nameHyphenated); // Keep only new questions
            })
            .map(({ title, topic, difficulty, description = null }, index) => {
              const nameHyphenated = title.replace(/\s+/g, '-').toLowerCase();
              const addedEpoch = Date.now();
              const idInt = parseInt(addedEpoch) + index;
              const id = `${idInt}`;

              return {
                id,
                name: title,
                nameHyphenated,
                topic,
                difficulty,
                description,
                code_templates: {},
                updated: addedEpoch,
                bookmarkStatus: false,
                delimeter: ' Welcome to OpenREPL!! you can start coding here. ',
              };
            });

          // Only update if there are new questions
          if (newQuestions.length > 0) {
            storedQuestions = [...storedQuestions, ...newQuestions];
            localStorage.setItem('questions', JSON.stringify(storedQuestions));
          }
          return newQuestions; // Return the list of new questions
        } catch (error) {
          console.error('Failed to fetch questions:', error);
          return []; // Return an empty array in case of error
        }
      }

      // Load stored questions and add them to the table.
      async function loadStoredQuestions() {
        // fetch sample questions from server if no questions are present
        fetchAndStoreQuestions().then(newQuestions => {
          console.log('Adding New Questions');
          newQuestions.forEach(q => addQuestionRow(q));
        }).catch(error => {
          console.error('Error fetching new questions:', error);
        });
        let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
        storedQuestions.forEach(q => addQuestionRow(q));
      }

      // Add a question row to the DataTable.
      function addQuestionRow(q) {
        // The "Last Updated" cell displays a human-readable date/time (using toLocaleString)
        // and uses a data-order attribute (with the epoch timestamp) for sorting.
        let newRow = `<tr data-id="${q.id}" data-difficulty="${q.difficulty}" data-added="${q.updated}"${q.bookmarkStatus ? ' class="bookmarked-row"' : ''}>
          <td><input type="checkbox" class="bookmark" ${q.bookmarkStatus ? 'checked' : ''}></td>
          <td><a href="/practice?name=${q.nameHyphenated}" class="question-link" target="_blank">${q.name}</a></td>
          <td>${q.topic}</td>
          <td>${q.difficulty}</td>
          <td>
            <div class="remarks-display">
              <span class="remarks-content">${q.remarks || 'Add remarks...'}</span>
              <i class="fa fa-pencil edit-icon"></i>
            </div>
          </td>
          <td data-order="${q.updated}">${new Date(q.updated).toLocaleString()}</td>
          <td><button class="delete-btn">🗑 Delete</button></td>
        </tr>`;
        table.row.add($(newRow)).draw(false);
        // Move newly added rows to the top
        let lastIndex = table.rows().count() - 1;
        let newRowNode = table.row(lastIndex).node();
        $(newRowNode).prependTo('#questionsTable tbody');

        // Ensure the topic is added to the filter dropdown if it's new
        if ($("#topicsFilter option[value='" + q.topic + "']").length === 0) {
          $("#topicsFilter").append(`<option value="${q.topic}">${q.topic}</option>`);
        }

        // Ensure the difficulty level is added to the filter dropdown if it's new
        if ($("#difficultyFilter option[value='" + q.difficulty + "']").length === 0) {
          $("#difficultyFilter").append(`<option value="${q.difficulty}">${q.difficulty}</option>`);
        }
      }

      function updateQuestionRow(q) {
          // Find the row in the DataTable by the question ID
          let row = table.row(`[data-id="${q.id}"]`);

          if (row.length) {
              // Update only the data in place, without replacing the entire row
              row.data([
                  `<input type="checkbox" class="bookmark" ${q.bookmarkStatus ? 'checked' : ''}>`,
                  `<a href="/practice?name=${q.nameHyphenated}" class="question-link" target="_blank">${q.name}</a>`,
                  q.topic,
                  q.difficulty,
                  `<div class="remarks-display">
                      <span class="remarks-content">${q.remarks || 'Add remarks...'}</span>
                      <i class="fa fa-pencil edit-icon"></i>
                  </div>`,
                  `<td data-order="${q.updated}">${new Date(q.updated).toLocaleString()}</td>`,
                  `<button class="delete-btn">🗑 Delete</button>`
              ]).draw(false); // Update the data and keep the current table state
          } else {
              console.warn('Row not found for question ID:', q.id);
          }
      }


      // Handle bookmark checkbox changes.
      $('#questionsTable tbody').on('change', '.bookmark', function() {
        let row = $(this).closest('tr');
        let rowId = row.attr('data-id');
        let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
        let question = storedQuestions.find(q => q.id === rowId);

        if (question) {
          question.bookmarkStatus = $(this).prop('checked');
          localStorage.setItem('questions', JSON.stringify(storedQuestions));
          updateBookmarks();
        }
      });


      // Click edit (pencil icon) → Convert cell to contenteditable
      $(document).on('click', '.edit-icon', function () {
        let cell = $(this).closest('td');
        let currentContent = cell.find('.remarks-content').html(); // Get current HTML content
        let rowElement = cell.closest('tr'); // Get the table row element
        let rowId = rowElement.attr('data-id'); // Extract the row ID

        // Replace display with editable content div
        cell.html(`
          <div contenteditable="true" class="remarks-editable">${currentContent}</div>
        `);
        let editableDiv = cell.find('.remarks-editable');
        editableDiv.focus();

        // Handle blur & touchend (for mobile)
        function saveRemarks() {
            let newContent = editableDiv.html().trim();
            let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
            
            // Persist to localStorage
            let question = storedQuestions.find(q => q.id === rowId);
            if (question) {
                question.remarks = newContent;
                question.updated = Date.now();
                localStorage.setItem('questions', JSON.stringify(storedQuestions));

                // Re-initialize the editable cell after update
                cell.html(`
                  <div class="remarks-display">
                    <span class="remarks-content">${newContent || 'Add remarks...'}</span>
                    <i class="fa fa-pencil edit-icon"></i>
                  </div>
                `);
                // Update the table row
                updateQuestionRow(question);
            }
        }

        editableDiv.on('blur', saveRemarks);
        editableDiv.on('touchend', saveRemarks); // Handle touchend for mobile
      });


      // Handle row deletion.
      $('#questionsTable tbody').on('click', '.delete-btn', function() {
        let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
        let row = $(this).closest('tr');
        let rowId = row.attr('data-id');
        storedQuestions = storedQuestions.filter(q => q.id !== rowId);
        localStorage.setItem('questions', JSON.stringify(storedQuestions));
        table.row(row).remove().draw();
        updateBookmarks();
      });

      // Handle sort/filter changes.
      $('#sortBy').on('change', function() {
        let selectedSort = $(this).val();
        if (selectedSort === "recent") {
          // Sort by the epoch timestamp (descending).
          table.order([5, 'desc']).draw();
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
        let storedQuestions = JSON.parse(localStorage.getItem('questions')) || [];
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